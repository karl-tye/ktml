; Scopes: a component and its inputs, a Render Clause and its bindings, and
; a Keyed Repetition and its item.
[
  (component_definition)
  (render_clause)
  (keyed_repetition)
] @local.scope

; Definitions.
(component_definition
  (parameter_list
    (parameter
      (identifier) @local.definition.parameter)))

(render_clause
  selector: (identifier) @local.definition.parameter)

(payload_binding
  alias: (identifier) @local.definition.var)

(payload_binding
  field: (identifier) @local.definition.var
  !alias)

(keyed_repetition
  item: (identifier) @local.definition.var)

(record_declaration
  name: (identifier) @local.definition.type)

(state_declaration
  name: (identifier) @local.definition.type)

(import_statement
  (identifier) @local.definition.import)

(fixture_declaration
  name: (identifier) @local.definition.constant)

; References: the root of every path, and component calls.
(interpolation
  (path
    .
    (identifier) @local.reference))

(path_condition
  (path
    .
    (identifier) @local.reference))

(presence_condition
  (path
    .
    (identifier) @local.reference))

(keyed_repetition
  collection: (path
    .
    (identifier) @local.reference))

(keyed_repetition
  key: (path
    .
    (identifier) @local.reference))

(spread_attribute
  (path
    .
    (identifier) @local.reference))

(element
  name: (identifier) @local.reference)

; A story names its component, and a value name may reference a fixture.
(story_declaration
  component: (identifier) @local.reference)

(value_name
  name: (identifier) @local.reference)
