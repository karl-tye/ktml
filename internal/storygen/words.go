package storygen

import (
	"strings"
	"unicode"
)

// category is what a field's name says its value is. Synthesis picks a
// plausible value for it, and redaction decides from it whether a captured
// value is sensitive.
type category int

const (
	categoryGeneric category = iota
	categoryClass
	categoryIcon
	categoryEmail
	categoryPhone
	categoryPostcode
	categoryAddress
	categoryCity
	categoryFirstName
	categoryLastName
	categoryFullName
	categoryCompany
	categoryID
	categorySecret
	categoryURL
	categoryDateTime
	categoryDate
	categoryTime
	categoryMoney
	categoryPercent
	categoryCount
	categoryStatus
	categoryTitle
	categoryFreeText
)

// words splits a lowerCamel, snake, or kebab name into lower-case words:
// classItemIconBgClass is class, item, icon, bg, class.
func words(name string) []string {
	var result []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			result = append(result, strings.ToLower(current.String()))
			current.Reset()
		}
	}
	runes := []rune(name)
	for index, r := range runes {
		switch {
		case r == '_' || r == '-' || r == '.' || unicode.IsSpace(r):
			flush()
			continue
		case unicode.IsUpper(r):
			// A run of capitals is one word: URLPath is url, path.
			nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			previousLower := index > 0 && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]))
			if previousLower || nextLower && index > 0 && unicode.IsUpper(runes[index-1]) {
				flush()
			}
		}
		current.WriteRune(r)
	}
	flush()
	return result
}

func has(list []string, wanted ...string) bool {
	for _, word := range list {
		for _, candidate := range wanted {
			if word == candidate {
				return true
			}
		}
	}
	return false
}

func last(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[len(list)-1]
}

// classify reads a field name. Markup-shaped names (classes and icons) win
// over data-shaped ones, so classItemIcon is markup, not an icon name; the
// last word decides among the rest, so customerName is a name and
// nameCount a count.
func classify(name string) category {
	list := words(name)
	switch {
	case len(list) == 0:
		return categoryGeneric
	case has(list, "class", "classes", "style", "css", "colour", "color"):
		return categoryClass
	case has(list, "icon"):
		return categoryIcon
	case has(list, "email", "mail"):
		return categoryEmail
	case has(list, "phone", "mobile", "tel", "telephone", "landline", "fax"):
		return categoryPhone
	case has(list, "postcode", "zip", "postal"):
		return categoryPostcode
	case has(list, "token", "secret", "password", "passcode", "otp", "pin", "signature", "nonce", "csrf"):
		return categorySecret
	}
	switch end := last(list); end {
	case "id", "ids", "uuid", "ref", "reference", "key", "number", "no", "sid", "guid":
		if end == "number" && has(list, "phone") {
			return categoryPhone
		}
		if end == "number" && !has(list, "account", "job", "order", "invoice", "reference", "policy", "booking", "ticket", "case") {
			return categoryCount
		}
		return categoryID
	case "url", "href", "link", "path", "src", "action", "uri", "endpoint", "route":
		return categoryURL
	case "address", "street", "line1", "line2", "addr", "location":
		return categoryAddress
	case "city", "town", "county", "region", "area":
		return categoryCity
	case "at", "timestamp", "datetime", "since", "until", "when":
		return categoryDateTime
	case "date", "day", "dob", "birthday", "deadline", "due":
		return categoryDate
	case "time", "hour", "slot":
		return categoryTime
	case "price", "amount", "cost", "fee", "fees", "balance", "total", "subtotal", "pay", "payment", "salary", "charge", "revenue", "spend", "budget", "value":
		if end == "value" && !has(list, "price", "amount", "money", "order", "invoice") {
			break
		}
		return categoryMoney
	case "percent", "percentage", "rate", "ratio", "share":
		return categoryPercent
	case "count", "counts", "qty", "quantity", "size", "length", "minutes", "hours", "days", "seconds", "age", "rank", "score", "level", "index", "position", "page", "pages", "limit", "offset", "year", "month", "week":
		return categoryCount
	case "status", "state", "stage", "kind", "type", "category", "role", "mode", "variant", "tier", "priority", "severity", "tone", "trade":
		return categoryStatus
	case "title", "heading", "subject", "headline", "subtitle":
		return categoryTitle
	case "description", "note", "notes", "message", "messages", "body", "text", "comment", "comments", "summary", "content", "reason", "details", "detail", "bio", "feedback", "review", "transcript", "answer", "question", "excerpt", "preview":
		return categoryFreeText
	case "firstname", "forename", "given":
		return categoryFirstName
	case "lastname", "surname", "family":
		return categoryLastName
	case "name", "names", "actor", "author", "owner", "customer", "engineer", "agent", "user", "contact", "person", "assignee", "recipient", "sender", "caller", "staff", "member":
		switch {
		case has(list, "first", "given", "fore"):
			return categoryFirstName
		case has(list, "last", "family", "sur"):
			return categoryLastName
		case has(list, "company", "business", "organisation", "organization", "org", "firm", "trading", "brand", "supplier", "account"):
			return categoryCompany
		case has(list, "file", "field", "input", "param", "event", "route", "class", "template", "tab", "column", "key",
			"role", "department", "team", "group", "trade", "category", "product", "plan", "service", "skill", "region", "area", "page", "nav", "menu", "item", "type", "kind", "status", "stage", "queue", "workflow", "campaign", "report", "policy", "certificate", "document", "tag"):
			return categoryGeneric
		}
		return categoryFullName
	case "company", "business", "organisation", "organization", "employer", "supplier", "firm":
		return categoryCompany
	}
	switch {
	case has(list, "created", "updated", "modified", "scheduled", "started", "ended", "completed", "booked", "sent", "received", "expires", "expiry"):
		return categoryDateTime
	case has(list, "first") && has(list, "name"), has(list, "forename"):
		return categoryFirstName
	}
	return categoryGeneric
}

// humanize turns a field name into a short label: itemActor is "Item actor".
func humanize(name string) string {
	list := words(name)
	if len(list) == 0 {
		return "Sample"
	}
	text := strings.Join(list, " ")
	return strings.ToUpper(text[:1]) + text[1:]
}

// negative reports a boolean name whose true value hides or disables
// something: noItems, hideHeader, isDisabled, loading.
func negative(name string) bool {
	return has(words(name), "no", "not", "hide", "hidden", "disabled", "disable", "empty", "loading", "error", "errors", "failed", "missing", "without", "skip", "readonly", "locked", "blocked", "archived", "deleted", "expired", "invalid", "collapsed")
}
