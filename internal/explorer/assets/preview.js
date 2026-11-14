// KTML Explorer preview hook. A preview has no application behind it: the
// Explorer answers its HTMX requests with 204 No Content and lists them in
// the Requests panel. This names the element and trigger of each request
// in headers, for htmx 2 (htmx:configRequest) and htmx 4
// (htmx:config:request).
(() => {
	// Header values must be Latin-1; keep them short printable ASCII.
	const ascii = (text) => String(text).replace(/[^\x20-\x7e]/g, "?").slice(0, 200);
	const describe = (element) => {
		if (!element || !element.tagName) {
			return "";
		}
		let name = element.tagName.toLowerCase();
		if (element.id) {
			name += "#" + element.id;
		} else if (element.classList && element.classList.length > 0) {
			name += "." + element.classList[0];
		}
		return name;
	};
	const tag = (headers, element, event) => {
		if (!headers) {
			return;
		}
		const name = describe(element);
		const trigger = (element && element.getAttribute && (element.getAttribute("hx-trigger") || element.getAttribute("data-hx-trigger"))) || (event && event.type) || "";
		if (name) {
			headers["X-KTML-Explorer-Element"] = ascii(name);
		}
		if (trigger) {
			headers["X-KTML-Explorer-Trigger"] = ascii(trigger);
		}
	};
	document.addEventListener("htmx:configRequest", (event) => {
		tag(event.detail.headers, event.target, event.detail.triggeringEvent);
	});
	document.addEventListener("htmx:config:request", (event) => {
		const ctx = event.detail.ctx;
		if (ctx && ctx.request) {
			tag(ctx.request.headers, ctx.sourceElement, ctx.sourceEvent);
		}
	});
})();
