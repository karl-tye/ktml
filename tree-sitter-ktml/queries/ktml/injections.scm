; Raw-text literals are the JavaScript of script elements and the CSS of
; style elements; the fences are excluded.
; A script without attributes holds JavaScript; one with attributes does
; too unless an attribute names JSON (see below). Neovim treats an absent
; capture as matching, so the two cases are separate patterns.
((element
  name: (identifier) @_name
  .
  child: (raw_text) @injection.content)
  (#match? @_name "^[sS][cC][rR][iI][pP][tT]$")
  (#offset! @injection.content 0 3 0 -3)
  (#set! injection.language "javascript"))

((element
  name: (identifier) @_name
  .
  child: (element_body (raw_text) @injection.content))
  (#match? @_name "^[sS][cC][rR][iI][pP][tT]$")
  (#offset! @injection.content 0 3 0 -3)
  (#set! injection.language "javascript"))

((element
  name: (identifier) @_name
  (attribute)+ @_attributes
  child: (raw_text) @injection.content)
  (#match? @_name "^[sS][cC][rR][iI][pP][tT]$")
  (#not-any-match? @_attributes "json")
  (#offset! @injection.content 0 3 0 -3)
  (#set! injection.language "javascript"))

((element
  name: (identifier) @_name
  (attribute)+ @_attributes
  child: (element_body (raw_text) @injection.content))
  (#match? @_name "^[sS][cC][rR][iI][pP][tT]$")
  (#not-any-match? @_attributes "json")
  (#offset! @injection.content 0 3 0 -3)
  (#set! injection.language "javascript"))

((element
  name: (identifier) @_name
  child: (raw_text) @injection.content)
  (#match? @_name "^[sS][tT][yY][lL][eE]$")
  (#offset! @injection.content 0 3 0 -3)
  (#set! injection.language "css"))

((element
  name: (identifier) @_name
  child: (element_body (raw_text) @injection.content))
  (#match? @_name "^[sS][tT][yY][lL][eE]$")
  (#offset! @injection.content 0 3 0 -3)
  (#set! injection.language "css"))

; A script element typed as JSON holds JSON.
((element
  name: (identifier) @_name
  (attribute
    (attribute_name) @_type
    (attribute_value
      (string_literal) @_value))
  child: (raw_text) @injection.content)
  (#match? @_name "^[sS][cC][rR][iI][pP][tT]$")
  (#eq? @_type "type")
  (#match? @_value "json")
  (#offset! @injection.content 0 3 0 -3)
  (#set! injection.language "json"))

; Alpine.js directives (x-*), event handlers (@event), and bindings (:attr)
; hold JavaScript expressions. KTML interpolations stay inside them.
((attribute
  (attribute_name) @_name
  (attribute_value
    [
      (string_literal)
      (interpolated_string)
    ] @injection.content))
  (#match? @_name "^x-")
  (#offset! @injection.content 0 1 0 -1)
  (#set! injection.language "javascript"))

((attribute
  (attribute_name) @_name
  (attribute_value
    [
      (string_literal)
      (interpolated_string)
    ] @injection.content))
  (#match? @_name "^[@]")
  (#offset! @injection.content 0 1 0 -1)
  (#set! injection.language "javascript"))

((attribute
  (attribute_name) @_name
  (attribute_value
    [
      (string_literal)
      (interpolated_string)
    ] @injection.content))
  (#match? @_name "^:")
  (#offset! @injection.content 0 1 0 -1)
  (#set! injection.language "javascript"))

; HTMX hx-vals and hx-headers values are JSON.
((attribute
  (attribute_name) @_name
  (attribute_value
    [
      (string_literal)
      (interpolated_string)
    ] @injection.content))
  (#any-of? @_name "hx-vals" "hx-headers" "data-hx-vals" "data-hx-headers")
  (#offset! @injection.content 0 1 0 -1)
  (#set! injection.language "json"))
