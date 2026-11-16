package livereload

// clientScript is injected into proxied HTML pages after the page's session
// token (see clientTag). It opens the session's SSE stream and applies:
//
//   - reload: a full page reload;
//   - patch: rerendered HTML of one root instance, matched by its Render
//     Identity (ADR-0004): a data-ktml-i attribute on a single-root element,
//     <!--ktml:c ID--> boundaries around a multi-root instance, or the whole
//     document for a doctype root;
//   - diagnostic/clear: the error overlay.
//
// Patches morph the live DOM. Keyed Repetition items (<!--ktml:item ID-->) and
// component instances are reconciled by identity, so surviving nodes move
// rather than being recreated and keep focus, selection, form values, and
// Alpine state. Removed Alpine scopes are destroyed, new ones initialized, and
// changed ones reinitialized with their state restored; inserted or changed
// HTMX content is processed. Content of other registry roots (HTMX fragments
// swapped into the page) is kept. Patches wait for in-flight HTMX requests. Any
// unbalanced, duplicate, or reshaped identity reloads the page.
const clientScript = `<script data-ktml-client>
(() => {
	if (window.__ktmlClient) return;
	window.__ktmlClient = true;
	const session = window.__ktmlSession || "";
	const BOUNDARIES = [["item", "ktml:item ", "/ktml:item "], ["c", "ktml:c ", "/ktml:c "]];

	class UnsafePatch extends Error {}

	const marker = (node) => {
		if (!node || node.nodeType !== Node.COMMENT_NODE) return null;
		for (const [kind, begin, end] of BOUNDARIES) {
			if (node.data.startsWith(begin)) return { begin: true, id: kind + " " + node.data.slice(begin.length) };
			if (node.data.startsWith(end)) return { begin: false, id: kind + " " + node.data.slice(end.length) };
		}
		return null;
	};

	// root is the registry root being patched. Content of other roots, such
	// as HTMX fragments swapped into the page, is not part of its render and
	// is kept.
	let root = "";
	const foreign = (segment) => {
		if (segment.id === null) return false;
		const identity = segment.id.slice(segment.id.indexOf(" ") + 1);
		const first = identity.split(" ")[0];
		return first.startsWith("@") && first !== root;
	};

	const instanceOf = (node) => node && node.nodeType === Node.ELEMENT_NODE && node.hasAttribute("data-ktml-i") ? "i " + node.getAttribute("data-ktml-i") : null;
	const pinned = (node) => node && node.nodeType === Node.ELEMENT_NODE && node.hasAttribute("data-ktml-client");

	// segments groups sibling nodes into keyed fragments (a begin marker through
	// its end marker), keyed instance elements, and single unkeyed nodes.
	const segments = (nodes) => {
		const result = [];
		const ids = new Set();
		const keep = (id) => {
			if (ids.has(id)) throw new UnsafePatch("duplicate identity " + id);
			ids.add(id);
		};
		for (let index = 0; index < nodes.length; index++) {
			const current = marker(nodes[index]);
			if (!current) {
				const id = instanceOf(nodes[index]);
				if (id !== null) keep(id);
				result.push({ id, nodes: [nodes[index]], element: id !== null });
				continue;
			}
			if (!current.begin) throw new UnsafePatch("unbalanced fragment end " + current.id);
			let depth = 0;
			let end = -1;
			for (let scan = index + 1; scan < nodes.length; scan++) {
				const inner = marker(nodes[scan]);
				if (!inner) continue;
				if (inner.begin) {
					depth++;
				} else if (depth > 0) {
					depth--;
				} else {
					if (inner.id !== current.id) throw new UnsafePatch("mismatched fragment end " + inner.id);
					end = scan;
					break;
				}
			}
			if (end < 0) throw new UnsafePatch("unclosed fragment " + current.id);
			keep(current.id);
			result.push({ id: current.id, nodes: nodes.slice(index, end + 1), element: false });
			index = end;
		}
		return result;
	};

	const compatible = (current, next, renamed) =>
		current.nodeType === next.nodeType &&
		(current.nodeType !== Node.ELEMENT_NODE || current.tagName === next.tagName) &&
		!marker(current) && !marker(next) && (renamed || instanceOf(current) === instanceOf(next));

	const kindOf = (id) => id.slice(0, id.indexOf(" "));

	// A patch records what it changed so the Alpine and HTMX lifecycles run
	// once it is applied.
	let change = null;

	const adopt = (node) => {
		const imported = document.importNode(node, true);
		change.inserted.push(imported);
		return imported;
	};

	const remove = (node) => {
		const alpine = Alpine();
		if (alpine && node.nodeType === Node.ELEMENT_NODE) alpine.destroyTree(node);
		change.removed++;
		if (node.parentNode && node.parentNode.nodeType === Node.ELEMENT_NODE) change.touched.add(node.parentNode);
		node.remove();
	};

	// morphNode updates current to match next and returns the live node. A
	// renamed instance element is morphed although its identity changed.
	const morphNode = (current, next, renamed) => {
		if (!compatible(current, next, renamed)) {
			const replacement = adopt(next);
			const alpine = Alpine();
			if (alpine && current.nodeType === Node.ELEMENT_NODE) alpine.destroyTree(current);
			current.replaceWith(replacement);
			return replacement;
		}
		if (current.nodeType !== Node.ELEMENT_NODE) {
			if (current.data !== next.data) {
				current.data = next.data;
				if (current.parentNode) change.touched.add(current.parentNode);
			}
			return current;
		}
		for (const attribute of Array.from(current.attributes)) {
			if (!next.hasAttribute(attribute.name)) {
				current.removeAttribute(attribute.name);
				change.touched.add(current);
			}
		}
		for (const attribute of Array.from(next.attributes)) {
			// Alpine removes x-cloak once it initializes an element; adding it
			// back would hide the element again.
			if (attribute.name === "x-cloak" && Alpine() && !current.hasAttribute("x-cloak")) continue;
			// Attribute changes never overwrite a control's live value or
			// checkedness once the user has edited it.
			if (current.getAttribute(attribute.name) !== attribute.value) {
				current.setAttribute(attribute.name, attribute.value);
				change.touched.add(current);
			}
		}
		reconcile(current, Array.from(current.childNodes), Array.from(next.childNodes), null);
		return current;
	};

	const place = (parent, node, anchor) => {
		if (node.parentNode === parent && typeof parent.moveBefore === "function") {
			parent.moveBefore(node, anchor);
		} else {
			parent.insertBefore(node, anchor);
		}
	};

	// longestIncreasing returns the plan entries whose old positions already
	// appear in order; only the other entries move.
	const longestIncreasing = (positions) => {
		const tails = [];
		const previous = new Array(positions.length).fill(-1);
		positions.forEach((position, index) => {
			if (position < 0) return;
			let low = 0;
			let high = tails.length;
			while (low < high) {
				const middle = (low + high) >> 1;
				if (positions[tails[middle]] < position) low = middle + 1;
				else high = middle;
			}
			if (low > 0) previous[index] = tails[low - 1];
			tails[low] = index;
		});
		const keep = new Set();
		for (let index = tails.length ? tails[tails.length - 1] : -1; index >= 0; index = previous[index]) keep.add(index);
		return keep;
	};

	// reconcile makes the live nodes (children of parent, before anchor) match
	// the next nodes. Keyed fragments and instance elements match by identity;
	// other nodes match in order with the next compatible unkeyed node. The
	// injected client is never touched.
	const reconcile = (parent, liveNodes, nextNodes, anchor) => {
		liveNodes = liveNodes.filter((node) => !pinned(node));
		nextNodes = nextNodes.filter((node) => !pinned(node));
		const live = segments(liveNodes);
		const next = segments(nextNodes);
		const keyed = new Map();
		const unkeyed = [];
		live.forEach((segment, position) => {
			segment.position = position;
			if (segment.id !== null) keyed.set(segment.id, segment);
			else unkeyed.push(segment);
		});
		// A component call or outlet added or removed before an instance
		// renumbers its c<n>/o<n> frame. An instance whose identity is gone is
		// matched, in order, with a new instance of the same shape, so its
		// nodes and their state survive. Repetition items keep their keys.
		const nextIds = new Set(next.filter((segment) => segment.id !== null).map((segment) => segment.id));
		const renamedFrom = (segment) => {
			if (kindOf(segment.id) === "item") return null;
			return live.find((candidate) => !candidate.used && candidate.id !== null && !foreign(candidate) && !nextIds.has(candidate.id) &&
				kindOf(candidate.id) === kindOf(segment.id) && candidate.element === segment.element &&
				(!segment.element || candidate.nodes[0].tagName === segment.nodes[0].tagName)) || null;
		};
		let cursor = 0;
		const plan = next.map((segment) => {
			if (segment.id !== null) {
				let match = keyed.get(segment.id);
				let renamed = false;
				if (!match || match.element !== segment.element) {
					match = renamedFrom(segment);
					renamed = match !== null;
				}
				if (!match) return { nodes: segment.nodes.map(adopt), position: -1 };
				match.used = true;
				if (match.element) {
					const node = morphNode(match.nodes[0], segment.nodes[0], renamed);
					return { nodes: [node], position: match.position };
				}
				const inner = segment.nodes.slice(1, -1);
				const end = match.nodes[match.nodes.length - 1];
				if (renamed) {
					match.nodes[0].data = segment.nodes[0].data;
					end.data = segment.nodes[segment.nodes.length - 1].data;
				}
				reconcile(parent, match.nodes.slice(1, -1), inner, end);
				const nodes = [];
				for (let node = match.nodes[0]; node; node = node.nextSibling) {
					nodes.push(node);
					if (node === end) break;
				}
				return { nodes, position: match.position };
			}
			while (cursor < unkeyed.length && !compatible(unkeyed[cursor].nodes[0], segment.nodes[0])) cursor++;
			if (cursor < unkeyed.length) {
				const match = unkeyed[cursor++];
				match.used = true;
				const node = morphNode(match.nodes[0], segment.nodes[0]);
				return { nodes: [node], position: node === match.nodes[0] ? match.position : -1 };
			}
			return { nodes: [adopt(segment.nodes[0])], position: -1 };
		});
		for (const segment of live) {
			if (segment.used || foreign(segment)) continue;
			for (const node of segment.nodes) remove(node);
		}
		const keep = longestIncreasing(plan.map((entry) => entry.position));
		let before = anchor;
		for (let index = plan.length - 1; index >= 0; index--) {
			const nodes = plan[index].nodes;
			if (!keep.has(index)) {
				for (const node of nodes) place(parent, node, before);
			}
			before = nodes[0];
		}
	};

	const Alpine = () => (window.Alpine && typeof window.Alpine.initTree === "function" ? window.Alpine : null);

	// alpineBound reports whether Alpine renders or binds an element, so a
	// change to it needs its scope reinitialized.
	const alpineBound = (element) => {
		for (const node of [element, element.parentElement]) {
			if (!node || node.nodeType !== Node.ELEMENT_NODE) continue;
			for (const attribute of Array.from(node.attributes)) {
				if (attribute.name.startsWith("x-") || attribute.name.startsWith(":") || attribute.name.startsWith("@")) return true;
			}
		}
		return false;
	};

	// snapshot saves the own state of every Alpine scope under root, and of
	// root's enclosing scopes.
	const snapshot = (root) => {
		const saved = new Map();
		const alpine = Alpine();
		if (!alpine) return saved;
		const scopes = new Set(root.querySelectorAll ? root.querySelectorAll("[x-data]") : []);
		for (let node = root; node && node.nodeType === Node.ELEMENT_NODE; node = node.parentElement) {
			if (node.hasAttribute("x-data")) scopes.add(node);
		}
		for (const scope of scopes) {
			const data = scope._x_dataStack && scope._x_dataStack[0];
			if (!data) continue;
			const state = {};
			for (const key of Object.keys(data)) {
				const value = data[key];
				if (typeof value !== "function") state[key] = typeof alpine.raw === "function" ? alpine.raw(value) : value;
			}
			saved.set(scope, { expression: scope.getAttribute("x-data"), state });
		}
		return saved;
	};

	const outermost = (elements) => Array.from(elements).filter((element) => !Array.from(elements).some((other) => other !== element && other.contains(element)));

	// lifecycle runs after a patch: Alpine scopes whose bound markup changed
	// are reinitialized with their state restored when their x-data is
	// unchanged, inserted nodes are initialized, and HTMX processes inserted
	// and changed content.
	const lifecycle = (saved) => {
		const inserted = change.inserted.filter((node) => node.isConnected && node.nodeType === Node.ELEMENT_NODE);
		const alpine = Alpine();
		if (alpine) {
			const reinit = new Set();
			for (const element of change.touched) {
				if (!element.isConnected || element.nodeType !== Node.ELEMENT_NODE || !alpineBound(element)) continue;
				const scope = element.closest("[x-data]");
				if (scope && !inserted.some((node) => node.contains(scope))) reinit.add(scope);
			}
			const roots = outermost(reinit);
			for (const root of roots) {
				alpine.destroyTree(root);
				alpine.initTree(root);
				for (const [scope, before] of saved) {
					if (!root.contains(scope) || !scope.isConnected || scope.getAttribute("x-data") !== before.expression) continue;
					const data = scope._x_dataStack && scope._x_dataStack[0];
					if (data) Object.assign(data, before.state);
				}
			}
			for (const node of outermost(inserted)) {
				if (!roots.some((root) => root.contains(node))) alpine.initTree(node);
			}
		}
		if (window.htmx && typeof window.htmx.process === "function") {
			for (const node of outermost(inserted)) window.htmx.process(node);
			for (const element of change.touched) {
				if (!element.isConnected || element.nodeType !== Node.ELEMENT_NODE) continue;
				if (Array.from(element.attributes).some((attribute) => attribute.name.startsWith("hx-") || attribute.name.startsWith("data-hx-"))) window.htmx.process(element);
			}
		}
	};

	const parse = (html) => {
		const template = document.createElement("template");
		template.innerHTML = html;
		return Array.from(template.content.childNodes).filter((node) => node.nodeType !== Node.TEXT_NODE || node.data.trim() !== "");
	};

	const findInstance = (identity) => {
		const elements = Array.from(document.querySelectorAll("[data-ktml-i]")).filter((element) => element.getAttribute("data-ktml-i") === identity);
		const walker = document.createTreeWalker(document, NodeFilter.SHOW_COMMENT);
		const begins = [];
		for (let node = walker.nextNode(); node; node = walker.nextNode()) {
			const current = marker(node);
			if (current && current.begin && current.id === "c " + identity) begins.push(node);
		}
		if (elements.length + begins.length > 1) throw new UnsafePatch("duplicate instance " + identity);
		if (elements.length) return { element: elements[0] };
		if (!begins.length) return null;
		const begin = begins[0];
		let depth = 0;
		for (let node = begin.nextSibling; node; node = node.nextSibling) {
			const inner = marker(node);
			if (!inner) continue;
			if (inner.begin) depth++;
			else if (depth > 0) depth--;
			else if (inner.id === "c " + identity) return { begin, end: node };
			else throw new UnsafePatch("mismatched instance end " + inner.id);
		}
		throw new UnsafePatch("unclosed instance " + identity);
	};

	const apply = (patch) => {
		const active = document.activeElement;
		let selection = null;
		try {
			if (active && typeof active.selectionStart === "number") selection = [active.selectionStart, active.selectionEnd, active.selectionDirection];
		} catch (_) {}
		change = { inserted: [], touched: new Set(), removed: 0 };
		root = patch.identity.split(" ")[0];
		const run = (body) => {
			const alpine = Alpine();
			if (alpine && typeof alpine.mutateDom === "function") alpine.mutateDom(body);
			else body();
		};
		if (patch.document) {
			const parsed = new DOMParser().parseFromString(patch.html, "text/html");
			const saved = snapshot(document.documentElement);
			run(() => morphNode(document.documentElement, parsed.documentElement));
			lifecycle(saved);
		} else {
			const target = findInstance(patch.identity);
			if (!target) {
				console.debug("[KTML] Instance " + patch.identity + " is no longer on the page");
				return;
			}
			const next = parse(patch.html);
			if (target.element) {
				if (next.length !== 1 || instanceOf(next[0]) !== "i " + patch.identity) throw new UnsafePatch("instance " + patch.identity + " changed shape");
				const saved = snapshot(target.element);
				run(() => morphNode(target.element, next[0]));
				lifecycle(saved);
			} else {
				const first = marker(next[0]);
				const last = marker(next[next.length - 1]);
				if (!first || !last || !first.begin || last.begin || first.id !== "c " + patch.identity || last.id !== first.id) throw new UnsafePatch("instance " + patch.identity + " changed shape");
				const live = [];
				for (let node = target.begin.nextSibling; node && node !== target.end; node = node.nextSibling) live.push(node);
				const parent = target.begin.parentNode;
				const saved = snapshot(parent);
				run(() => reconcile(parent, live, next.slice(1, -1), target.end));
				lifecycle(saved);
			}
		}
		if (active && active !== document.body && active.isConnected && document.activeElement !== active) {
			active.focus({ preventScroll: true });
			if (selection) {
				try { active.setSelectionRange(selection[0], selection[1], selection[2]); } catch (_) {}
			}
		}
	};

	// Patches wait while HTMX requests are in flight, so a patch never strips
	// request classes or races a swap.
	let inFlight = 0;
	const queue = [];
	const reload = () => window.location.reload();
	const flush = () => {
		while (inFlight === 0 && queue.length) {
			const patch = queue.shift();
			try {
				apply(patch);
			} catch (error) {
				console.error("[KTML] Unsafe patch; reloading", error);
				reload();
				return;
			}
		}
	};
	const patch = (value) => {
		queue.push(value);
		flush();
	};
	document.addEventListener("htmx:configRequest", (event) => {
		if (session) event.detail.headers["X-KTML-Session"] = session;
	});
	document.addEventListener("htmx:beforeRequest", () => { inFlight++; });
	document.addEventListener("htmx:afterRequest", () => {
		inFlight = Math.max(0, inFlight - 1);
		if (inFlight === 0) setTimeout(flush, 100);
	});

	const OVERLAY = "ktml-error-overlay";
	const showError = (message) => {
		let host = document.getElementById(OVERLAY);
		if (!host) {
			host = document.createElement("div");
			host.id = OVERLAY;
			host.setAttribute("data-ktml-client", "");
			host.style.cssText = "position:fixed;inset:0;z-index:2147483647;background:rgba(15,23,42,.88);color:#fecaca;font:13px/1.5 ui-monospace,monospace;padding:32px;overflow:auto";
			host.attachShadow({ mode: "open" });
			document.documentElement.appendChild(host);
		}
		host.shadowRoot.innerHTML = "<strong>KTML: the last valid page is still shown</strong><pre></pre>";
		// CSSOM styles, which a page's style-src does not restrict.
		host.shadowRoot.querySelector("strong").style.color = "#fff";
		host.shadowRoot.querySelector("pre").style.whiteSpace = "pre-wrap";
		host.shadowRoot.querySelector("pre").textContent = message;
	};
	const clearError = () => {
		const host = document.getElementById(OVERLAY);
		if (host) host.remove();
	};

	// reloadAssets applies a dev.reload_watch change. Changed stylesheets
	// are swapped in place: each same-origin <link rel=stylesheet> whose
	// file name matches is replaced, once the new copy loads, by one with a
	// cache-busting query; when none matches, every same-origin stylesheet
	// is refreshed (the file may be @imported). Any other asset, or a page
	// without stylesheets to refresh, reloads.
	const reloadAssets = (paths) => {
		const fileName = (path) => path.split("/").pop();
		if (!paths.length || !paths.every((path) => /\.css$/i.test(path))) {
			console.log("[KTML] Assets changed; reloading", paths);
			reload();
			return;
		}
		const names = new Set(paths.map(fileName));
		const links = Array.from(document.querySelectorAll('link[rel~="stylesheet"][href]')).filter((link) => {
			try {
				return new URL(link.href, location.href).origin === location.origin;
			} catch (error) {
				return false;
			}
		});
		const matching = links.filter((link) => names.has(fileName(new URL(link.href, location.href).pathname)));
		const targets = matching.length ? matching : links;
		if (!targets.length) {
			reload();
			return;
		}
		for (const link of targets) {
			const url = new URL(link.href, location.href);
			url.searchParams.set("ktml-reload", String(Date.now()));
			const next = link.cloneNode(false);
			// The changed file no longer matches a subresource integrity hash.
			next.removeAttribute("integrity");
			next.href = url.href;
			const done = () => link.remove();
			next.addEventListener("load", done, { once: true });
			next.addEventListener("error", done, { once: true });
			link.after(next);
		}
		console.log("[KTML] Reloaded stylesheets", paths);
	};

	// The stream closes when the page is hidden: a page kept in the
	// back/forward cache would otherwise hold its connection, and browsers
	// allow only six per host, so a few navigations would stall every
	// request. A page restored from that cache missed its patches, so it
	// reloads.
	let sse = null;
	const connect = () => {
		sse = new EventSource("/__ktml/livereload?session=" + encodeURIComponent(session));
		sse.addEventListener("reload", () => {
			console.log("[KTML] Received full reload event");
			reload();
		});
		sse.addEventListener("patch", (event) => {
			let value;
			try {
				value = JSON.parse(event.data);
			} catch (error) {
				console.error("[KTML] Malformed patch; reloading", error);
				reload();
				return;
			}
			patch(value);
		});
		sse.addEventListener("assets", (event) => {
			let value;
			try {
				value = JSON.parse(event.data);
			} catch (error) {
				console.error("[KTML] Malformed asset change; reloading", error);
				reload();
				return;
			}
			reloadAssets(value.paths || []);
		});
		sse.addEventListener("diagnostic", (event) => showError(event.data));
		sse.addEventListener("clear", clearError);
	};
	window.addEventListener("pagehide", () => {
		if (sse) sse.close();
		sse = null;
	});
	window.addEventListener("pageshow", (event) => {
		if (event.persisted) reload();
	});
	connect();
	window.__ktmlPatch = patch;
})();
</script>`
