// KTML Explorer interface. The server renders every panel and types every
// value; this script only sends control edits and swaps in what comes back,
// filters and remembers the story tree, lists the preview's requests, and
// opens a story chosen in the tree without reloading the page.
(() => {
	// story is the selected story; a story chosen in the tree replaces it.
	let story = document.body.dataset.story;
	const panel = document.querySelector("[data-panel]");
	const valuesNode = document.querySelector("[data-values]");
	let values = valuesNode ? JSON.parse(valuesNode.textContent || "null") || {} : {};
	let pending = Promise.resolve();

	const encode = (object) => {
		const bytes = new TextEncoder().encode(JSON.stringify(object));
		let binary = "";
		bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
		return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
	};

	// load points the preview at a new URL in place, so the frame adds no
	// entry to the session history and Back leaves the story.
	const load = (preview) => {
		const frame = document.querySelector("[data-preview]");
		if (!frame || !preview || (frame.dataset.src || frame.getAttribute("src")) === preview) {
			return;
		}
		frame.dataset.src = preview;
		try {
			frame.contentWindow.location.replace(new URL(preview, location.href).href);
		} catch {
			frame.setAttribute("src", preview);
		}
	};

	// An edit belongs to the story it was made in; one still queued when
	// another story opens is dropped.
	const send = (edit) => {
		const editing = story;
		pending = pending.then(async () => {
			if (editing !== story) {
				return;
			}
			const response = await fetch("/_explorer/edit", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ story, values, edit }),
			});
			if (!response.ok) {
				return;
			}
			const result = await response.json();
			values = result.values;
			if (result.controls !== undefined && result.controls !== "") {
				panel.innerHTML = result.controls;
			}
			const error = panel.querySelector("[data-edit-error]");
			if (error) {
				error.remove();
			}
			if (result.error) {
				panel.insertAdjacentHTML("afterbegin", '<p class="control-error" data-edit-error></p>');
				panel.querySelector("[data-edit-error]").textContent = result.error;
			}
			load(result.preview);
		});
	};

	const editOf = (control) => {
		const path = JSON.parse(control.dataset.path);
		const op = control.dataset.op;
		if (op !== "set") {
			return { op, path, value: control.value };
		}
		switch (control.dataset.kind) {
		case "boolean":
			return { op, path, value: control.checked };
		case "number":
		case "integer": {
			if (control.value === "" || !control.checkValidity()) {
				return null;
			}
			return { op, path, value: Number(control.value) };
		}
		case "fixture":
			return { op, path, value: { fixture: control.value } };
		default:
			return { op, path, value: control.value };
		}
	};

	// The story tree. Groups open as the viewer left them in this browser,
	// plus the selected story's path; a search shows only the matching
	// stories, marks the matches, and opens their groups.
	const nav = document.querySelector("[data-navigation]");
	const search = document.querySelector("[data-search]");
	const noResults = document.querySelector("[data-no-results]");
	const openKey = "ktml-explorer:open";
	const queryKey = "ktml-explorer:query";
	const stored = (storage, key, fallback) => {
		try {
			const value = storage.getItem(key);
			return value === null ? fallback : value;
		} catch {
			return fallback;
		}
	};
	const store = (storage, key, value) => {
		try {
			storage.setItem(key, value);
		} catch {
			// Storage is unavailable; the tree then starts from the server's.
		}
	};
	let remembered;
	try {
		remembered = new Set(JSON.parse(stored(localStorage, openKey, "[]")));
	} catch {
		remembered = new Set();
	}
	let tree = [];
	let routeTexts = [];
	let routes = [];

	const labelOf = (group) => group.querySelector(":scope > summary > [data-label]");
	const index = () => {
		const routeNode = nav.querySelector("[data-route-index]");
		routeTexts = routeNode ? JSON.parse(routeNode.textContent || "[]") : [];
		routes = routeTexts.map((route) => route.toLowerCase());
		const node = (group) => ({ group, label: labelOf(group), text: labelOf(group).textContent });
		tree = [...nav.querySelectorAll(".kind")].map((kind) => ({
			...node(kind),
			packages: [...kind.querySelectorAll(":scope > .package")].map((pkg) => ({
				...node(pkg),
				components: [...pkg.querySelectorAll(":scope > .component")].map((component) => ({
					...node(component),
					route: component.querySelector("[data-route]"),
					routes: (component.dataset.routes || "").split(" ").filter(Boolean).map(Number),
					stories: [...component.querySelectorAll("[data-story-item]")].map((item) => {
						const label = item.querySelector("[data-label]");
						return { item, label, text: label.textContent };
					}),
				})),
			})),
		}));
	};

	// setOpen marks a change the script makes, so the toggle it fires is not
	// remembered as the viewer's.
	const setOpen = (group, open) => {
		if (group.open !== open) {
			group.dataset.auto = "";
			group.open = open;
		}
	};
	const setHidden = (node, hidden) => {
		if (node.hidden !== hidden) {
			node.hidden = hidden;
		}
	};
	const mark = (node, text, query) => {
		const at = query ? text.toLowerCase().indexOf(query) : -1;
		if (at < 0) {
			if (node.firstElementChild) {
				node.textContent = text;
			}
			return false;
		}
		const match = document.createElement("mark");
		match.textContent = text.slice(at, at + query.length);
		node.replaceChildren(text.slice(0, at), match, text.slice(at + query.length));
		return true;
	};

	const filter = () => {
		const query = search ? search.value.trim().toLowerCase() : "";
		store(sessionStorage, queryKey, query);
		const matchingRoutes = query ? routes.map((route) => route.includes(query)) : [];
		let shown = 0;
		for (const kind of tree) {
			let kindShown = false;
			for (const pkg of kind.packages) {
				const packageMatch = mark(pkg.label, pkg.text, query);
				let packageShown = false;
				for (const component of pkg.components) {
					const componentMatch = mark(component.label, component.text, query);
					const route = query ? component.routes.find((i) => matchingRoutes[i]) : undefined;
					if (route === undefined) {
						setHidden(component.route, true);
						if (component.route.firstChild) {
							component.route.textContent = "";
						}
					} else {
						mark(component.route, routeTexts[route], query);
						setHidden(component.route, false);
					}
					const whole = !query || packageMatch || componentMatch || route !== undefined;
					let componentShown = false;
					for (const story of component.stories) {
						const visible = mark(story.label, story.text, query) || whole;
						setHidden(story.item, !visible);
						if (visible) {
							componentShown = true;
							shown++;
						}
					}
					setHidden(component.group, !componentShown);
					packageShown = packageShown || componentShown;
				}
				setHidden(pkg.group, !packageShown);
				kindShown = kindShown || packageShown;
			}
			setHidden(kind.group, !kindShown);
		}
		for (const group of nav.querySelectorAll("details[data-group]")) {
			setOpen(group, query ? !group.hidden : group.hasAttribute("data-current") || remembered.has(group.dataset.group));
		}
		if (noResults) {
			setHidden(noResults, shown > 0 || tree.length === 0);
		}
	};

	if (nav) {
		nav.addEventListener("toggle", (event) => {
			const group = event.target;
			if (!(group instanceof HTMLDetailsElement) || !group.dataset.group) {
				return;
			}
			if ("auto" in group.dataset) {
				delete group.dataset.auto;
				return;
			}
			if (search && search.value.trim()) {
				return;
			}
			if (group.open) {
				remembered.add(group.dataset.group);
			} else {
				remembered.delete(group.dataset.group);
			}
			store(localStorage, openKey, JSON.stringify([...remembered].sort()));
		}, true);
		if (search) {
			search.value = stored(sessionStorage, queryKey, "");
			search.addEventListener("input", filter);
			search.addEventListener("keydown", (event) => {
				if (event.key === "Escape") {
					search.value = "";
					filter();
				}
			});
			document.addEventListener("keydown", (event) => {
				const target = event.target;
				const typing = target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName);
				if (event.key === "/" && !typing && !event.ctrlKey && !event.metaKey && !event.altKey) {
					event.preventDefault();
					search.focus();
					search.select();
				}
			});
		}
		index();
		filter();
		const current = nav.querySelector("a[aria-current]");
		if (current) {
			current.scrollIntoView({ block: "nearest" });
		}
	}

	if (panel && story) {
		const timers = new WeakMap();
		const handle = (event, delay) => {
			const control = event.target.closest("[data-op]");
			if (!control || control.tagName === "BUTTON") {
				return;
			}
			const edit = editOf(control);
			if (!edit) {
				return;
			}
			clearTimeout(timers.get(control));
			timers.set(control, setTimeout(() => send(edit), delay));
		};
		panel.addEventListener("input", (event) => {
			if (event.target.type === "text" || event.target.type === "number") {
				handle(event, 250);
			}
		});
		panel.addEventListener("change", (event) => handle(event, 0));
		panel.addEventListener("click", (event) => {
			const button = event.target.closest("button[data-op]");
			if (button) {
				send({ op: button.dataset.op, path: JSON.parse(button.dataset.path) });
			}
		});
	}

	// The Requests panel lists the preview's HTMX requests, which the
	// Explorer answers with 204 No Content: newest first, repeats counted.
	// A refresh asked for while one runs runs once after it.
	const requestsPanel = document.querySelector("[data-requests]");
	let requestsBusy = false;
	let requestsAgain = false;
	const renderRequests = (requests) => {
		const list = requestsPanel.querySelector("[data-request-list]");
		const rows = requests.map((request) => {
			const row = document.createElement("li");
			row.dataset.request = "";
			row.dataset.count = String(request.count);
			const cell = (className, text) => {
				const node = document.createElement(className === "time" ? "time" : "span");
				node.className = className;
				node.textContent = text;
				return node;
			};
			const by = [request.element, request.trigger].filter(Boolean).join(" · ");
			row.append(cell("method", request.method), cell("url", request.url));
			row.querySelector(".url").title = request.url;
			if (by) {
				row.append(cell("by", by));
			}
			if (request.count > 1) {
				row.append(cell("repeats", "×" + request.count));
			}
			const time = cell("time", new Date(request.time).toLocaleTimeString());
			time.dateTime = request.time;
			row.append(time);
			return row;
		});
		list.replaceChildren(...rows);
		const total = requests.reduce((sum, request) => sum + request.count, 0);
		requestsPanel.querySelector("[data-request-total]").textContent = total ? "(" + total + ")" : "";
		requestsPanel.querySelector("[data-requests-empty]").hidden = requests.length > 0;
	};
	const refreshRequests = async () => {
		if (requestsBusy) {
			requestsAgain = true;
			return;
		}
		requestsBusy = true;
		try {
			do {
				requestsAgain = false;
				const response = await fetch("/_explorer/requests?" + new URLSearchParams({ story }).toString());
				if (response.ok) {
					renderRequests((await response.json()).requests || []);
				}
			} while (requestsAgain);
		} catch {
			// The Explorer stopped; the panel keeps what it showed.
		} finally {
			requestsBusy = false;
		}
	};
	if (requestsPanel && story) {
		requestsPanel.querySelector("[data-requests-clear]").addEventListener("click", async () => {
			await fetch("/_explorer/requests?" + new URLSearchParams({ story }).toString(), { method: "DELETE" });
			refreshRequests();
		});
		refreshRequests();
	}

	// A story chosen in the tree opens without reloading the page, so the
	// tree keeps its search, open groups, and scroll position: the URL is
	// pushed, and the title, controls, diagnostics, values, and preview are
	// swapped in from /_explorer/state. Back and Forward open their story the
	// same way. A link opened with a modifier key or another button, or a
	// page without a preview to swap, navigates as a link.
	const select = (key) => {
		for (const node of nav.querySelectorAll("a[aria-current]")) {
			node.removeAttribute("aria-current");
		}
		for (const group of nav.querySelectorAll("details[data-current]")) {
			delete group.dataset.current;
		}
		const link = [...nav.querySelectorAll("[data-story-item] a")].find((a) => new URL(a.href).searchParams.get("story") === key);
		if (!link) {
			return;
		}
		link.setAttribute("aria-current", "page");
		for (let group = link.closest("details"); group; group = group.parentElement.closest("details")) {
			group.dataset.current = "";
		}
	};
	// openStory swaps in the story a URL names. The new preview is a fresh
	// frame: the old one's document unloads, closing its streams, and
	// pointing the old frame elsewhere would add an entry to the session
	// history.
	const openStory = (params) => {
		pending = pending.then(async () => {
			const query = new URLSearchParams({ story: params.get("story") || "", v: params.get("v") || "" });
			let state;
			try {
				const response = await fetch("/_explorer/state?" + query.toString());
				state = response.ok ? await response.json() : null;
			} catch {
				state = null;
			}
			const frame = document.querySelector("[data-preview]");
			if (!state || !state.story || !frame) {
				window.location.reload();
				return;
			}
			story = state.story;
			values = state.values || {};
			document.body.dataset.story = story;
			document.title = state.title + " · KTML Explorer";
			const title = document.querySelector(".title");
			title.querySelector("h1").textContent = state.title;
			title.querySelector(".source").textContent = state.source;
			title.querySelector(".reset").setAttribute("href", "/?story=" + encodeURIComponent(story));
			const next = frame.cloneNode(false);
			delete next.dataset.src;
			next.setAttribute("src", state.preview);
			next.setAttribute("title", "Preview of " + state.title);
			frame.replaceWith(next);
			panel.innerHTML = state.controls;
			document.querySelector("[data-diagnostics]").innerHTML = state.diagnostics;
			select(story);
			if (requestsPanel) {
				renderRequests([]);
				refreshRequests();
			}
		});
		return pending;
	};
	if (nav && panel && document.querySelector("[data-preview]")) {
		nav.addEventListener("click", (event) => {
			const link = event.target.closest("a[href]");
			if (!link || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || (link.target && link.target !== "_self")) {
				return;
			}
			const url = new URL(link.href);
			if (url.origin !== location.origin || url.pathname !== "/" || !url.searchParams.has("story")) {
				return;
			}
			event.preventDefault();
			if (url.search !== location.search) {
				history.pushState(null, "", url.pathname + url.search);
				openStory(url.searchParams);
			}
		});
		window.addEventListener("popstate", () => openStory(new URLSearchParams(location.search)));
	}

	// A workspace change refreshes the panels; the preview itself is patched
	// in place by the HMR client inside the frame.
	// The stream closes when the page is hidden, so a page kept in the
	// back/forward cache holds no connection (browsers allow six per host);
	// a restored page may be stale, so it reloads.
	const events = new EventSource("/_explorer/events");
	window.addEventListener("pagehide", () => events.close());
	window.addEventListener("pageshow", (event) => {
		if (event.persisted) {
			window.location.reload();
		}
	});
	events.addEventListener("requests", (event) => {
		if (requestsPanel && story && event.data === story) {
			refreshRequests();
		}
	});
	events.addEventListener("changed", () => {
		pending = pending.then(async () => {
			const query = new URLSearchParams({ story: story || "", v: encode(values) });
			const response = await fetch("/_explorer/state?" + query.toString());
			if (!response.ok) {
				return;
			}
			const state = await response.json();
			nav.innerHTML = state.navigation;
			index();
			filter();
			document.querySelector("[data-diagnostics]").innerHTML = state.diagnostics;
			if (panel && story) {
				const focused = document.activeElement && panel.contains(document.activeElement);
				if (!focused) {
					panel.innerHTML = state.controls;
				}
				values = state.values || values;
			}
		});
	});
})();
