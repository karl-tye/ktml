package storygen

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
)

// Redactor replaces the sensitive values of captured props with fakes. A
// fake is chosen by a keyed hash of the category and the raw value, so the
// same value always gets the same fake under one key, and the fake does not
// reveal the value to anyone without the key.
type Redactor struct {
	key []byte
}

// NewRedactor returns a Redactor with a secret key.
func NewRedactor(key []byte) *Redactor {
	return &Redactor{key: append([]byte(nil), key...)}
}

// CaptureKeyPath is where the redaction key of this user lives: outside any
// project, so it is never committed, and kept across runs, so fakes stay
// stable.
func CaptureKeyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ktml", "capture.key"), nil
}

// LoadCaptureKey reads the user's redaction key, creating it (0600) the
// first time.
func LoadCaptureKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if key, err := hex.DecodeString(strings.TrimSpace(string(data))); err == nil && len(key) >= 32 {
			return key, nil
		}
		return nil, fmt.Errorf("%s is not a redaction key; delete it to create a new one", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadCaptureKey(path)
		}
		return nil, err
	}
	defer file.Close()
	if _, err := file.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		return nil, err
	}
	return key, nil
}

// hash is the keyed hash of a raw value in a category.
func (r *Redactor) hash(kind, raw string) uint64 {
	mac := hmac.New(sha256.New, r.key)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(raw))
	return binary.BigEndian.Uint64(mac.Sum(nil))
}

func (r *Redactor) hex(kind, raw string, digits int) string {
	return fmt.Sprintf("%016x", r.hash(kind, raw))[:digits]
}

func (r *Redactor) pick(options []string, kind, raw string) string {
	return options[r.hash(kind, raw)%uint64(len(options))]
}

// Values redacts a captured story's input values.
func (r *Redactor) Values(values []compiler.StoryValue) []compiler.StoryValue {
	result := make([]compiler.StoryValue, len(values))
	for index, value := range values {
		result[index] = compiler.StoryValue{Input: value.Input, Value: r.Value(value.Input, value.Value)}
	}
	return result
}

// Value redacts one value; name is its input or field name.
func (r *Redactor) Value(name string, value compiler.Value) compiler.Value {
	switch value.Kind {
	case compiler.RecordValueKind, compiler.VariantValueKind:
		if value.Fields != nil {
			fields := make([]compiler.FieldValue, len(value.Fields))
			for index, field := range value.Fields {
				fields[index] = compiler.FieldValue{Name: field.Name, Value: r.Value(field.Name, field.Value)}
			}
			value.Fields = fields
		}
	case compiler.CollectionValueKind:
		items := make([]compiler.Value, len(value.Items))
		for index, item := range value.Items {
			items[index] = r.Value(name, item)
		}
		value.Items = items
	case compiler.ScalarValueKind, compiler.FixtureValueKind:
		if value.Type.Kind == compiler.ContextualTypeKind {
			value.String = r.contextual(value.Type.Name, name, value.String)
			return value
		}
		switch value.Type.Scalar {
		case compiler.StringType:
			value.String = r.String(name, value.String)
		case compiler.IntegerType:
			if classify(name) == categoryID {
				value.Integer = int64(100000 + r.hash("integer-id", fmt.Sprint(value.Integer))%900000)
				value.Number = float64(value.Integer)
			}
		case compiler.NumberType:
			if classify(name) == categoryID {
				value.Number = float64(100000 + r.hash("number-id", fmt.Sprint(value.Number))%900000)
			}
		}
	}
	return value
}

var (
	emailPattern    = regexp.MustCompile(`(?i)[a-z0-9._%+'-]+@[a-z0-9.-]+\.[a-z]{2,}`)
	phonePattern    = regexp.MustCompile(`(?:\+|\b0)[0-9][0-9 ()-]{7,}[0-9]`)
	postcodePattern = regexp.MustCompile(`(?i)\b[A-Z]{1,2}[0-9][A-Z0-9]? ?[0-9][A-Z]{2}\b`)
	uuidPattern     = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	longHexPattern  = regexp.MustCompile(`(?i)\b[0-9a-f]{16,}\b`)
	digitsPattern   = regexp.MustCompile(`[0-9]{5,}`)
	tokenPattern    = regexp.MustCompile(`\b[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b|\b(?:sk|pk|rk|key|tok)_[A-Za-z0-9]{12,}\b`)
)

// String redacts a string field. Markup (classes, icons) and coarse values
// (statuses, counts, amounts, dates) are kept unless the text itself looks
// like personal data; names, contact details, identifiers, secrets, URLs,
// and free text are replaced.
func (r *Redactor) String(name, raw string) string {
	if raw == "" {
		return raw
	}
	category := classify(name)
	switch category {
	case categoryClass, categoryIcon:
		return raw
	case categoryEmail:
		return r.email(raw)
	case categoryPhone:
		return r.phone(raw)
	case categoryPostcode:
		return r.pick(postcodes, "postcode", raw)
	case categoryAddress:
		return r.pick(streets, "address", raw)
	case categoryCity:
		return r.pick(cities, "city", raw)
	case categoryFirstName:
		return r.pick(firstNames, "first", raw)
	case categoryLastName:
		return r.pick(lastNames, "last", raw)
	case categoryFullName:
		return r.pick(firstNames, "first", raw) + " " + r.pick(lastNames, "last", raw)
	case categoryCompany:
		return r.pick(companies, "company", raw)
	case categoryID:
		return r.identifier(name, raw)
	case categorySecret:
		return "redacted-" + r.hex("secret", raw, 8)
	case categoryURL:
		return r.url(raw)
	case categoryDate:
		if has(words(name), "dob", "birth", "birthday") {
			return r.pick(dates, "date", raw)
		}
	}
	if scrubbed, changed := r.patterns(raw); changed {
		return scrubbed
	}
	switch category {
	case categoryStatus, categoryCount, categoryMoney, categoryPercent, categoryDateTime, categoryDate, categoryTime:
		return raw
	case categoryTitle:
		return r.pick(titles, "title", raw)
	}
	// Free text and anything unnamed may hold personal data.
	if len(raw) > 120 {
		return longText
	}
	if category == categoryFreeText || strings.Contains(strings.TrimSpace(raw), " ") {
		return r.pick(sentences, "text", raw)
	}
	return humanize(name) + " " + r.hex("text", raw, 4)
}

// patterns replaces personal data found in any text.
func (r *Redactor) patterns(raw string) (string, bool) {
	result := raw
	result = emailPattern.ReplaceAllStringFunc(result, r.email)
	result = uuidPattern.ReplaceAllStringFunc(result, func(match string) string { return "id-" + r.hex("uuid", match, 8) })
	result = tokenPattern.ReplaceAllStringFunc(result, func(match string) string { return "redacted-" + r.hex("secret", match, 8) })
	result = longHexPattern.ReplaceAllStringFunc(result, func(match string) string { return r.hex("hex", match, 16) })
	result = phonePattern.ReplaceAllStringFunc(result, r.phone)
	result = postcodePattern.ReplaceAllStringFunc(result, func(match string) string { return r.pick(postcodes, "postcode", match) })
	result = digitsPattern.ReplaceAllStringFunc(result, func(match string) string {
		return fmt.Sprintf("%0*d", len(match), r.hash("digits", match)%pow10(len(match)))
	})
	return result, result != raw
}

func pow10(digits int) uint64 {
	result := uint64(1)
	for range min(digits, 18) {
		result *= 10
	}
	return result
}

func (r *Redactor) email(raw string) string {
	first, lastName := r.pick(firstNames, "email-first", raw), r.pick(lastNames, "email-last", raw)
	return fmt.Sprintf("%s.%s%d@example.com", strings.ToLower(first), strings.ToLower(lastName), r.hash("email", raw)%100)
}

func (r *Redactor) phone(raw string) string {
	// Ofcom reserves 07700 900000-900999 for drama.
	return fmt.Sprintf("07700 900%03d", r.hash("phone", raw)%1000)
}

func (r *Redactor) identifier(name, raw string) string {
	prefix := strings.Join(words(name), "-")
	if prefix == "" {
		prefix = "id"
	}
	return prefix + "-" + r.hex("id", raw, 8)
}

// url keeps a URL's shape: its scheme and the path segments that name
// routes. Hosts become example.com, identifying segments fakes, and the
// query and fragment are dropped.
func (r *Redactor) url(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "/"
	}
	switch strings.ToLower(parsed.Scheme) {
	case "mailto":
		return "mailto:" + r.email(parsed.Opaque)
	case "tel", "sms":
		return strings.ToLower(parsed.Scheme) + ":" + strings.ReplaceAll(r.phone(parsed.Opaque), " ", "")
	case "", "http", "https":
	default:
		return "/"
	}
	result := url.URL{Scheme: parsed.Scheme}
	if parsed.Host != "" {
		result.Host = "example.com"
	}
	segments := strings.Split(parsed.Path, "/")
	for index, segment := range segments {
		if identifying(segment) {
			segments[index] = "id-" + r.hex("segment", segment, 8)
		}
	}
	result.Path = strings.Join(segments, "/")
	text := result.String()
	if text == "" {
		return "/"
	}
	return text
}

// identifying reports a path segment that names a record rather than a
// route: it has a digit, an @, or is long.
func identifying(segment string) bool {
	return strings.ContainsAny(segment, "0123456789@") || len(segment) > 24
}

// contextual redacts a trusted or contextual value.
func (r *Redactor) contextual(typeName, name, raw string) string {
	switch typeName {
	case compiler.URLType, compiler.TrustedURLType:
		if raw == "" {
			return raw
		}
		return r.url(raw)
	case compiler.JSONValueType:
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return "null"
		}
		encoded, err := json.Marshal(r.json(name, decoded))
		if err != nil {
			return "null"
		}
		return string(encoded)
	case compiler.TrustedScriptType:
		// Scripts built in Go may embed data; keep only those without
		// literals.
		if strings.ContainsAny(raw, "'\"`@") || digitsPattern.MatchString(raw) || len(raw) > 200 {
			return "{}"
		}
		return raw
	case compiler.CSSValueType:
		if strings.Contains(strings.ToLower(raw), "url(") || strings.ContainsAny(raw, "'\"") {
			return ""
		}
		return raw
	}
	return raw
}

func (r *Redactor) json(name string, value any) any {
	switch value := value.(type) {
	case string:
		return r.String(name, value)
	case float64:
		if classify(name) == categoryID {
			return float64(100000 + r.hash("json-id", fmt.Sprint(value))%900000)
		}
		return value
	case []any:
		for index, item := range value {
			value[index] = r.json(name, item)
		}
		return value
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value[key] = r.json(key, value[key])
		}
		return value
	}
	return value
}
