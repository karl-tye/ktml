package livereload

import "encoding/json"

// Picker configures the component picker that ktml dev adds to the client
// (internal/dev/picker). The Explorer's own previews never get it.
type Picker struct {
	// Hotkey toggles pick mode, as config.Dev.PickerHotkey spells it.
	Hotkey string `json:"hotkey"`
	// Explorer is the Component Explorer's base URL.
	Explorer string `json:"explorer"`
}

// SetPicker adds the component picker to the client of every page proxied
// from now on.
func (s *Server) SetPicker(picker Picker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.picker = &picker
}

// pickerTag is the picker's configuration and script.
func pickerTag(picker *Picker) string {
	if picker == nil {
		return ""
	}
	// encoding/json escapes <, >, and &, so the JSON is safe in a script.
	encoded, _ := json.Marshal(picker)
	return "\n<script data-ktml-client>window.__ktmlPicker = " + string(encoded) + ";</script>\n" + pickerScript
}

// pickerScript is the component picker. The hotkey toggles pick mode; Esc
// leaves it. In pick mode:
//
//   - hovering highlights the innermost component instance under the
//     pointer (its data-ktml-i element or ktml:c range; ADR-0004) and labels
//     it with its component chain, the current level in bold;
//   - Shift+wheel up selects the enclosing instance and down goes back
//     toward the innermost, one level per gesture (Alt+Shift+Up/Down do the
//     same); the page does not scroll; moving to another instance resets
//     the level;
//   - a click opens the selected component's story in one reused Explorer
//     tab, after ktml dev creates a type-derived story if it has none. The
//     click, and the pointer events around it, never reach the page.
//
// The page's identities go to ktml dev's /__ktml/picker/ routes, which name
// their components; no props leave the application.
const pickerScript = `<script data-ktml-client>
(() => {
	const options = window.__ktmlPicker;
	if (!options || window.__ktmlPickerLoaded) return;
	window.__ktmlPickerLoaded = true;
	const TAB = "ktml-explorer";
	// A wheel gesture ends after this many quiet milliseconds; it steps at
	// most one level, once its movement passes the threshold.
	const GESTURE_GAP = 150;
	const WHEEL_THRESHOLD = 24;

	const parts = options.hotkey.split("+");
	const key = parts.pop();
	const held = (name) => parts.includes(name);
	const code = /^[A-Z]$/.test(key) ? "Key" + key : /^[0-9]$/.test(key) ? "Digit" + key : key;
	const isHotkey = (event) => event.code === code && event.ctrlKey === held("Ctrl") && event.altKey === held("Alt") &&
		event.shiftKey === held("Shift") && event.metaKey === held("Meta");

	const decode = (text) => text.replace(/%3C/g, "<").replace(/%3E/g, ">").replace(/%25/g, "%");
	const BEGIN = "ktml:c ";
	const END = "/ktml:c ";
	const own = (node) => node && node.nodeType === Node.ELEMENT_NODE && node.closest("[data-ktml-client]");

	// instancesAt lists the marked instances that contain node, innermost
	// first: elements with data-ktml-i, and ktml:c ranges among the
	// preceding siblings at each level.
	const instancesAt = (node) => {
		const found = [];
		for (let current = node; current && current !== document; current = current.parentNode) {
			if (current.nodeType === Node.ELEMENT_NODE && current.hasAttribute("data-ktml-i")) found.push(current.getAttribute("data-ktml-i"));
			let depth = 0;
			for (let sibling = current.previousSibling; sibling; sibling = sibling.previousSibling) {
				if (sibling.nodeType !== Node.COMMENT_NODE) continue;
				if (sibling.data.startsWith(END)) depth++;
				else if (sibling.data.startsWith(BEGIN)) {
					if (depth === 0) found.push(decode(sibling.data.slice(BEGIN.length)));
					else depth--;
				}
			}
		}
		return found;
	};

	const pageIdentities = () => {
		const ids = new Set();
		for (const element of document.querySelectorAll("[data-ktml-i]")) ids.add(element.getAttribute("data-ktml-i"));
		const walker = document.createTreeWalker(document, NodeFilter.SHOW_COMMENT);
		for (let node = walker.nextNode(); node; node = walker.nextNode()) {
			if (node.data.startsWith(BEGIN)) ids.add(decode(node.data.slice(BEGIN.length)));
		}
		return Array.from(ids);
	};

	// chains maps an identity to its component chain (outermost first), or
	// null when it names no component; components describes each component.
	const chains = new Map();
	const components = {};
	let resolving = null;
	let again = false;
	const post = (route, body) => fetch("/__ktml/picker/" + route, {
		method: "POST",
		credentials: "same-origin",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	}).then((response) => {
		if (!response.ok) return response.text().then((text) => { throw new Error(text.trim() || response.statusText); });
		return response.json();
	});
	const resolve = () => {
		if (resolving) {
			again = true;
			return;
		}
		const identities = pageIdentities().filter((identity) => !chains.has(identity));
		if (!identities.length) return;
		resolving = post("resolve", { identities }).then((answer) => {
			Object.assign(components, answer.components || {});
			for (const identity of identities) chains.set(identity, (answer.chains || {})[identity] || null);
		}).catch((error) => {
			console.error("[KTML] The picker could not resolve the page's components", error);
			for (const identity of identities) chains.set(identity, null);
		}).finally(() => {
			resolving = null;
			if (again) {
				again = false;
				resolve();
			}
			render();
		});
	};

	// documentChain is the chain of the page's document components, for a
	// pointer outside every marked instance.
	const documentChain = () => {
		for (const identity of pageIdentities()) {
			const chain = chains.get(identity);
			if (!chain) continue;
			const prefix = [];
			for (const entry of chain) {
				if (!(components[entry.component] || {}).document) break;
				prefix.push(entry);
			}
			if (prefix.length) return prefix;
		}
		return null;
	};

	let host = null;
	let box = null;
	let label = null;
	let cursor = null;
	const show = () => {
		host = document.createElement("div");
		host.setAttribute("data-ktml-client", "");
		host.setAttribute("data-ktml-picker", "");
		host.style.cssText = "position:fixed;inset:0;pointer-events:none;z-index:2147483646";
		const root = host.attachShadow({ mode: "open" });
		// Styles go through the CSSOM, which a page's style-src does not
		// restrict, rather than style attributes or a style element.
		root.innerHTML = "<div data-box></div><div data-label><div data-path></div><div data-hint>Click to open in the Explorer · Shift+scroll or Alt+Shift+↑↓ to walk up and down · Esc to exit</div></div>";
		root.querySelector("[data-box]").style.cssText = "position:fixed;display:none;box-sizing:border-box;border:2px solid #6366f1;background:rgba(99,102,241,.12);border-radius:3px";
		root.querySelector("[data-label]").style.cssText = "position:fixed;left:8px;bottom:8px;max-width:calc(100vw - 16px);box-sizing:border-box;padding:6px 10px;border-radius:6px;background:#1e1b4b;color:#e0e7ff;font:12px/1.4 ui-sans-serif,system-ui,sans-serif;box-shadow:0 4px 16px rgba(0,0,0,.3)";
		root.querySelector("[data-hint]").style.opacity = ".7";
		box = root.querySelector("[data-box]");
		label = root.querySelector("[data-path]");
		label.textContent = "KTML picker: point at a component";
		document.documentElement.appendChild(host);
		cursor = new CSSStyleSheet();
		cursor.replaceSync("*{cursor:crosshair!important}");
		document.adoptedStyleSheets = [...document.adoptedStyleSheets, cursor];
	};
	const hide = () => {
		if (host) host.remove();
		if (cursor) document.adoptedStyleSheets = document.adoptedStyleSheets.filter((sheet) => sheet !== cursor);
		host = box = label = cursor = null;
	};

	let active = false;
	// hovered is the innermost instance under the pointer ("" outside every
	// instance); level indexes its chain, null for the innermost.
	let hovered = null;
	let level = null;
	let target = null;

	const currentChain = () => {
		if (hovered === null) return { chain: null, pending: false };
		if (hovered === "") return { chain: documentChain(), pending: !!resolving };
		const chain = chains.get(hovered);
		return { chain: chain || null, pending: chain === undefined };
	};

	const rectOf = (identity) => {
		const element = document.querySelector("[data-ktml-i=\"" + CSS.escape(identity) + "\"]");
		if (element) return element.getBoundingClientRect();
		const walker = document.createTreeWalker(document, NodeFilter.SHOW_COMMENT);
		let begin = null;
		for (let node = walker.nextNode(); node; node = walker.nextNode()) {
			if (!begin && node.data === BEGIN + identity.replace(/%/g, "%25").replace(/</g, "%3C").replace(/>/g, "%3E")) begin = node;
			else if (begin && node.data === END + begin.data.slice(BEGIN.length) && node.parentNode === begin.parentNode) {
				const range = document.createRange();
				range.setStartAfter(begin);
				range.setEndBefore(node);
				return range.getBoundingClientRect();
			}
		}
		// A document component has no marker: it is the whole page.
		return new DOMRect(0, 0, document.documentElement.clientWidth, window.innerHeight);
	};

	const render = () => {
		if (!active || !label) return;
		const { chain, pending } = currentChain();
		target = null;
		if (!chain || !chain.length) {
			box.style.display = "none";
			label.textContent = pending ? "KTML picker: …" : hovered === null ? "KTML picker: point at a component" : "KTML picker: no component here";
			return;
		}
		if (level === null || level >= chain.length) level = chain.length - 1;
		if (level < 0) level = 0;
		target = chain[level];
		const rect = rectOf(target.identity);
		Object.assign(box.style, { display: "block", left: rect.left + "px", top: rect.top + "px", width: Math.max(rect.width, 2) + "px", height: Math.max(rect.height, 2) + "px" });
		label.textContent = "";
		chain.forEach((entry, index) => {
			if (index) label.appendChild(document.createTextNode(" › "));
			const name = (components[entry.component] || {}).name || entry.component;
			const part = document.createElement(index === level ? "b" : "span");
			part.textContent = name;
			if (index === level) part.style.color = "#fff";
			label.appendChild(part);
		});
		if (!(components[target.component] || {}).story) {
			const note = document.createElement("span");
			note.style.opacity = ".7";
			note.textContent = "  (no story yet: a click creates one from its types)";
			label.appendChild(note);
		}
	};

	const hover = (node) => {
		if (own(node)) return;
		const found = instancesAt(node);
		let innermost = "";
		for (const identity of found) {
			const chain = chains.get(identity);
			if (chain === undefined) resolve();
			if (chain !== null) {
				innermost = identity;
				break;
			}
		}
		if (innermost !== hovered) {
			hovered = innermost;
			level = null;
		}
		render();
	};

	const step = (direction) => {
		const { chain } = currentChain();
		if (!chain || !chain.length) return;
		if (level === null) level = chain.length - 1;
		level = Math.min(chain.length - 1, Math.max(0, level + direction));
		render();
	};

	const flash = (message) => {
		if (label) label.textContent = message;
	};

	const open = (entry) => {
		const info = components[entry.component] || {};
		const url = options.explorer + "/_explorer/open?component=" + encodeURIComponent(entry.component);
		if (info.story) {
			window.open(url, TAB);
			stop();
			return;
		}
		// The tab opens now, while the click allows it; it is pointed at the
		// story once ktml dev has created it.
		const tab = window.open("", TAB);
		flash("KTML picker: creating a story for " + (info.name || entry.component) + "…");
		post("open", { component: entry.component }).then((answer) => {
			components[entry.component] = Object.assign({}, info, { story: true });
			if (tab && !tab.closed) tab.location.href = answer.url;
			else window.open(answer.url, TAB);
			stop();
		}).catch((error) => {
			console.error("[KTML] The picker could not open " + entry.component, error);
			flash("KTML picker: " + error.message);
		});
	};

	const start = () => {
		if (active) return;
		active = true;
		hovered = null;
		level = null;
		chains.clear();
		show();
		resolve();
	};
	const stop = () => {
		active = false;
		hovered = null;
		hide();
	};

	let wheelTotal = 0;
	let wheelStepped = false;
	let wheelTimer = null;
	const onWheel = (event) => {
		if (!active || !event.shiftKey) return;
		event.preventDefault();
		event.stopImmediatePropagation();
		clearTimeout(wheelTimer);
		wheelTimer = setTimeout(() => {
			wheelTotal = 0;
			wheelStepped = false;
		}, GESTURE_GAP);
		if (wheelStepped) return;
		// Browsers may turn a Shift+wheel into horizontal movement.
		wheelTotal += Math.abs(event.deltaY) >= Math.abs(event.deltaX) ? event.deltaY : event.deltaX;
		if (Math.abs(wheelTotal) >= WHEEL_THRESHOLD) {
			wheelStepped = true;
			step(wheelTotal < 0 ? -1 : 1);
		}
	};

	const swallow = (event) => {
		if (!active) return;
		event.preventDefault();
		event.stopImmediatePropagation();
	};

	window.addEventListener("keydown", (event) => {
		if (isHotkey(event)) {
			event.preventDefault();
			event.stopImmediatePropagation();
			if (active) stop();
			else start();
			return;
		}
		if (!active) return;
		if (event.key === "Escape") {
			swallow(event);
			stop();
		} else if (event.altKey && event.shiftKey && (event.key === "ArrowUp" || event.key === "ArrowDown")) {
			swallow(event);
			step(event.key === "ArrowUp" ? -1 : 1);
		}
	}, true);
	window.addEventListener("pointermove", (event) => {
		if (active) hover(event.composedPath()[0] || event.target);
	}, true);
	window.addEventListener("wheel", onWheel, { capture: true, passive: false });
	for (const name of ["pointerdown", "pointerup", "mousedown", "mouseup", "dblclick", "auxclick", "contextmenu"]) {
		window.addEventListener(name, swallow, true);
	}
	window.addEventListener("click", (event) => {
		if (!active) return;
		swallow(event);
		hover(event.composedPath()[0] || event.target);
		if (target) open(target);
	}, true);
	window.addEventListener("scroll", () => render(), true);
	window.addEventListener("resize", () => render());
})();
</script>`
