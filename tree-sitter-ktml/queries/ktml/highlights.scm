(import_statement
  "import" @keyword.import
  "from" @keyword.import)

(import_alias "as" @keyword.import)

(component_definition
  (identifier) @constructor)

(parameter
  (identifier) @variable.parameter)

"record" @keyword.type

(record_declaration
  name: (identifier) @type.definition)

(field_declaration
  name: (identifier) @property)

(field_declaration
  "?" @punctuation.special)

(type
  (identifier) @type)

(collection_type
  "[" @punctuation.bracket
  "]" @punctuation.bracket)

(override_declaration
  name: (identifier) @property)

"state" @keyword.type

(state_declaration
  name: (identifier) @type.definition)

(variant_declaration
  name: (identifier) @constant)

(component_definition
  "default" @keyword)

"fixture" @keyword

(fixture_declaration
  name: (identifier) @constant)

"story" @keyword

(story_declaration
  component: (identifier) @constructor)

(story_declaration
  "." @punctuation.delimiter)

(story_declaration
  name: (identifier) @function)

(value_argument
  name: (identifier) @property)

; A value name is a Variant or a fixture reference, both constants; None is
; the absent Option.
(value_name
  name: (identifier) @constant)

((value_name
  name: (identifier) @constant.builtin)
  (#eq? @constant.builtin "None"))

[
  (story_arguments)
  (value_payload)
  (record_value)
  (list_value)
] @punctuation.bracket

(render_clause
  name: (identifier) @constructor)

(render_clause
  selector: (identifier) @variable.parameter)

(pattern
  qualifier: (identifier) @type)

(pattern
  name: (identifier) @constant)

(pattern
  "." @punctuation.delimiter)

(payload_binding
  field: (identifier) @property)

(payload_binding
  "as" @keyword)

(payload_binding
  alias: (identifier) @variable)

(element
  name: (identifier) @tag)

(attribute_name) @property

(doctype
  "doctype" @keyword)

(doctype
  name: (identifier) @type)

(html_comment) @comment

(raw_text) @string

(spread_attribute
  "..." @operator)

(attribute_bag
  "(" @punctuation.bracket
  ")" @punctuation.bracket)

(element_id) @label

(attribute_group
  "(" @punctuation.bracket
  ")" @punctuation.bracket)

(string_literal) @string
(interpolated_string) @string
(boolean_literal) @boolean
(number_literal) @number

(interpolation
  "{" @punctuation.special
  (path (identifier) @variable)
  "}" @punctuation.special)

(attribute_value
  (identifier) @variable)

(comment) @comment

(condition_node
  "if" @keyword.conditional)

(condition_node
  "else" @keyword.conditional)

(attribute_condition
  "if" @keyword.conditional)

(attribute_condition
  "else" @keyword.conditional)

[
  "!"
  "&&"
  "||"
] @operator

(presence_condition
  "?" @operator)

(path_condition
  (path (identifier) @variable))

(presence_condition
  (path (identifier) @variable))

(group_condition
  "(" @punctuation.bracket
  ")" @punctuation.bracket)

(slot_fill
  "slot" @keyword)

(slot_fill
  name: (identifier) @variable.parameter)

(keyed_repetition
  "for" @keyword.repeat
  "in" @keyword.repeat
  "key" @keyword.repeat)

(keyed_repetition
  item: (identifier) @variable.parameter)

(keyed_repetition
  collection: (path (identifier) @variable))

(keyed_repetition
  key: (path (identifier) @variable))

(keyed_repetition
  (path "." @punctuation.delimiter))
