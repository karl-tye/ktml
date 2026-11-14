[
  (element_body)
  (attribute_group)
  (attribute_bag)
  (attribute_block)
  (parameter_list)
  (record_declaration)
  (state_declaration)
  (variant_payload)
  (story_arguments)
  (value_payload)
  (record_value)
  (list_value)
] @indent.begin

[
  "}"
  "]"
  ")"
] @indent.branch

[
  "}"
  "]"
  ")"
] @indent.end

[
  (comment)
] @indent.ignore

; Script and style content keeps the author's indentation.
(raw_text) @indent.auto
