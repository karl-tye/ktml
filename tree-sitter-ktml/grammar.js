const IDENTIFIER = /[a-zA-Z_][a-zA-Z0-9_\-]*/;

// Attribute names (attribute-names-doctype-comments-v1, widened by
// migration-language-v1): an optional @ or :, then an identifier start, then
// letters, digits, -, _, :, ., and *.
const ATTRIBUTE_NAME = /[@:]?[a-zA-Z_][a-zA-Z0-9_\-:.*]*/;

module.exports = grammar({
  name: 'ktml',

  extras: $ => [
    $.comment,
    /\s/
  ],

  externals: $ => [
    $._same_line,
    $._same_line_value,
    $._slot_keyword,
    $._condition_operand,
    $._payload_start,
    $._error_sentinel
  ],

  word: $ => $.identifier,

  // `else`, `for`, and `doctype` cannot name an element, so a stray `else`
  // and a top-level `for` or `doctype` are syntax errors as in the Go parser.
  // Identifiers are otherwise unreserved.
  reserved: {
    global: $ => [],
    element_name: $ => ['else', 'for', 'doctype'],
  },

  rules: {
    source_file: $ => repeat($._statement),

    _statement: $ => choice(
      $.import_statement,
      $.record_declaration,
      $.state_declaration,
      $.fixture_declaration,
      $.story_declaration,
      $.component_definition,
      $.render_clause,
      $.element
    ),

    import_statement: $ => seq(
      'import',
      '{',
      optional(commaSep1Trailing(choice($.identifier, $.import_alias))),
      '}',
      'from',
      $.string_literal
    ),

    import_alias: $ => seq(
      $.identifier,
      'as',
      $.identifier
    ),

    // Host Schema record. Document-kind placement is semantic, so records
    // parse at the top level of every document.
    record_declaration: $ => seq(
      'record',
      field('name', $.identifier),
      '[',
      optional(commaSep1Trailing($.field_declaration)),
      ']'
    ),

    field_declaration: $ => seq(
      field('name', $.identifier),
      optional('?'),
      ':',
      field('type', $.type)
    ),

    // Closed Presentation State. Like records, states parse at the top level
    // of every document; `state` is a keyword only where a top-level
    // declaration starts, so fields and elements may still be named `state`.
    state_declaration: $ => seq(
      'state',
      field('name', $.identifier),
      '{',
      optional(commaSep1Trailing($.variant_declaration)),
      '}'
    ),

    variant_declaration: $ => seq(
      field('name', $.identifier),
      optional(field('payload', $.variant_payload))
    ),

    // A payload uses the record field list. An empty `[]` is syntactically
    // valid and rejected semantically.
    variant_payload: $ => seq(
      '[',
      optional(commaSep1Trailing($.field_declaration)),
      ']'
    ),

    // Story fixture: `fixture name: Type = literal`. Like records and states,
    // stories and fixtures parse at the top level of every document; the
    // keyword counts only where a top-level declaration starts, so elements,
    // attributes, and fields may still be named `story` or `fixture`.
    fixture_declaration: $ => seq(
      'fixture',
      field('name', $.identifier),
      ':',
      field('type', $.type),
      '=',
      field('value', $._scalar_value)
    ),

    // `story Component.Name [input = value, …] { body }`. The argument list
    // and the body are both optional, and either may start on a later line.
    story_declaration: $ => seq(
      'story',
      field('component', $.identifier),
      '.',
      field('name', $.identifier),
      optional(field('arguments', $.story_arguments)),
      optional(field('body', $.element_body))
    ),

    story_arguments: $ => seq(
      '[',
      optional(commaSep1Trailing($.value_argument)),
      ']'
    ),

    // `name = value`: a story input, a record field, or a payload field.
    value_argument: $ => seq(
      field('name', $.identifier),
      '=',
      field('value', $._story_value)
    ),

    // A story value literal, typed by the input or field it is given for.
    _story_value: $ => choice(
      $._scalar_value,
      $.value_name,
      $.record_value,
      $.list_value
    ),

    _scalar_value: $ => choice(
      $.string_literal,
      $.number_literal,
      $.boolean_literal
    ),

    // A Variant, `None`, or a fixture reference, by context. A `[` on the
    // name's line opens a Variant payload; the scanner's `_payload_start`
    // rejects a `[` on a later line, as the Go parser does.
    value_name: $ => seq(
      field('name', $.identifier),
      optional(seq($._payload_start, field('payload', $.value_payload)))
    ),

    value_payload: $ => seq(
      '[',
      optional(commaSep1Trailing($.value_argument)),
      ']'
    ),

    // A record literal: the first entry is `name =`.
    record_value: $ => seq(
      '[',
      commaSep1Trailing($.value_argument),
      ']'
    ),

    // A collection literal. `[]` is an empty record or collection by context.
    list_value: $ => seq(
      '[',
      optional(commaSep1Trailing($._story_value)),
      ']'
    ),

    // A component declaration with a body, a bodyless Component Signature
    // (ends at its `]`), or a signature with `default` and a residual body.
    // `default` is a keyword only directly after the input list.
    component_definition: $ => prec(3, seq(
      $.identifier,
      $.parameter_list,
      optional(choice(
        $.element_body,
        seq(field('default', 'default'), $.element_body)
      ))
    )),

    parameter_list: $ => seq(
      '[',
      optional(commaSep1Trailing(choice($.parameter, $.override_declaration))),
      ']'
    ),

    parameter: $ => choice(
      seq($.identifier, '?', ':', $.type),
      seq($.identifier, ':', $.type, optional(seq('=', $.default_value)))
    ),

    // Legacy gohtml-only override. Only a string or number value keeps this
    // form: `name = identifier` as the first entry is a Render Clause header.
    override_declaration: $ => seq(
      field('name', $.identifier),
      '=',
      field('value', choice($.string_literal, $.number_literal))
    ),

    // Render Clause: `Name [selector = Pattern, field, field as local] { }`.
    // Bindings are comma separated without a trailing comma.
    render_clause: $ => seq(
      field('name', $.identifier),
      '[',
      field('selector', $.identifier),
      '=',
      field('pattern', $.pattern),
      repeat(seq(',', field('binding', $.payload_binding))),
      ']',
      field('body', $.element_body)
    ),

    // A Variant, optionally qualified by its state. `true`, `false`, `Some`,
    // and `None` are plain identifiers here.
    pattern: $ => choice(
      field('name', $.identifier),
      seq(field('qualifier', $.identifier), '.', field('name', $.identifier))
    ),

    payload_binding: $ => seq(
      field('field', $.identifier),
      optional(seq('as', field('alias', $.identifier)))
    ),

    // A named type (scalar, contextual, or record name, resolved
    // semantically) or a bracketed collection type.
    type: $ => choice(
      $.identifier,
      $.collection_type
    ),

    collection_type: $ => seq(
      '[',
      $.type,
      ']'
    ),

    default_value: $ => choice(
      $.string_literal,
      $.boolean_literal,
      $.number_literal
    ),

    // Elements follow the Go parser: a header (name, attributes, and the
    // opening token of the child) on one line. A newline ends the attribute
    // list; only a parenthesized continuation group spans lines. The external
    // `_same_line` token succeeds only when the next token is on the same
    // line and can continue the header.

    element: $ => seq(
      field('name', reserved('element_name', $.identifier)),
      optional(choice(
        seq($._same_line, field('attributes', $.attribute_group)),
        repeat1(seq($._same_line, choice($.attribute, $.spread_attribute)))
      )),
      optional(seq($._same_line, field('child', choice($.element_body, $.text, $.raw_text))))
    ),

    attribute_group: $ => seq(
      '(',
      repeat($._group_entry),
      ')'
    ),

    _group_entry: $ => choice(
      alias($._group_attribute, $.attribute),
      $.attribute_condition,
      $.spread_attribute
    ),

    // `...path` forwards an Attributes value. No space is allowed inside it.
    spread_attribute: $ => seq(
      '...',
      alias($._spread_path, $.path)
    ),

    _spread_path: $ => seq(
      alias(token.immediate(IDENTIFIER), $.identifier),
      repeat(seq(
        token.immediate('.'),
        alias(token.immediate(IDENTIFIER), $.identifier)
      ))
    ),

    // `name=( entries )`: the attribute bag literal of an Attributes input.
    attribute_bag: $ => seq(
      '(',
      repeat($._group_entry),
      ')'
    ),

    // A conditional attribute group, only inside a parenthesized attribute
    // list: `if condition { attributes } else { attributes }`. As in a body,
    // `if =` is an attribute named if.
    attribute_condition: $ => seq(
      'if',
      field('condition', $._condition),
      field('consequence', $.attribute_block),
      optional(seq(
        'else',
        field('alternative', choice($.attribute_block, $.attribute_condition))
      ))
    ),

    attribute_block: $ => seq(
      '{',
      repeat($._group_entry),
      '}'
    ),

    element_id: $ => seq('#', $.identifier),

    element_body: $ => seq(
      '{',
      repeat($._element_content),
      '}'
    ),

    // Body nodes. Conditions, Keyed Repetitions, and slot fills are body
    // nodes only, so a top-level `if`, `for`, or `slot` is a syntax error.
    _element_content: $ => choice(
      $.element,
      $.text,
      $.condition_node,
      $.keyed_repetition,
      $.slot_fill,
      $.doctype,
      $.html_comment,
      $.raw_text
    ),

    // `doctype name`, with the name on the keyword's line. Only `html` is
    // valid; that is checked semantically, as in the Go compiler.
    doctype: $ => seq(
      'doctype',
      $._same_line,
      field('name', $.identifier)
    ),

    // `<!-- … -->` ends at the first `-->`: no content piece can end in a
    // dash, so `-->` cannot occur inside it.
    html_comment: $ => token(seq(
      '<!--',
      repeat(choice(/[^-]/, /-[^-]/, /--+[^->]/)),
      /-*-->/
    )),

    // ``` … ``` ends at the first ```: no content piece can contain three
    // backticks in a row. It is valid only as script or style content, which
    // is checked semantically.
    raw_text: $ => token(seq(
      '```',
      repeat(choice(/[^`]/, /`[^`]/, /``[^`]/)),
      '```'
    )),

    text: $ => choice($.string_literal, $.interpolated_string),

    // A same-line attribute. The shorthand `name "value"` keeps its value on
    // the attribute's line; as in the Go parser, a string or number directly
    // after an attribute name is that attribute's value, never a text child.
    // The scanner's `_same_line_value` decides this deterministically.
    attribute: $ => choice(
      $.element_id,
      seq($.attribute_name, '=', $.attribute_value),
      seq($.attribute_name, $._same_line_value, choice($.string_literal, $.interpolated_string, $.number_literal)),
      $.attribute_name
    ),

    attribute_name: $ => ATTRIBUTE_NAME,

    // Inside a continuation group, newlines are whitespace.
    _group_attribute: $ => choice(
      $.element_id,
      seq(alias('if', $.attribute_name), '=', $.attribute_value),
      seq($.attribute_name, '=', $.attribute_value),
      prec(1, seq($.attribute_name, choice($.string_literal, $.interpolated_string, $.number_literal))),
      $.attribute_name
    ),

    attribute_value: $ => choice(
      $.attribute_bag,
      $.string_literal,
      $.identifier,
      $.number_literal,
      $.interpolated_string
    ),

    // `slot name { … }`. The external scanner recognizes `slot` as a keyword
    // only when an identifier with no attribute value after it follows on the
    // same line, so `slot name="x"` stays the HTML slot element.
    slot_fill: $ => seq(
      alias($._slot_keyword, 'slot'),
      field('name', $.identifier),
      field('body', $.element_body)
    ),

    // Conditions: the closed boolean/presence grammar.
    condition_node: $ => seq(
      'if',
      field('condition', $._condition),
      field('consequence', $.element_body),
      optional(seq(
        'else',
        field('alternative', choice($.element_body, $.condition_node))
      ))
    ),

    _condition: $ => choice(
      $.or_condition,
      $.and_condition,
      $._unary_condition
    ),

    or_condition: $ => prec.left(1, seq(
      field('left', $._condition),
      '||',
      field('right', $._condition)
    )),

    and_condition: $ => prec.left(2, seq(
      field('left', $._condition),
      '&&',
      field('right', $._condition)
    )),

    _unary_condition: $ => choice(
      $.not_condition,
      $._operand
    ),

    not_condition: $ => seq('!', $._operand),

    _operand: $ => choice(
      $.presence_condition,
      $.path_condition,
      $.group_condition
    ),

    // The `_condition_operand` guard rejects the literals `true` and `false`
    // as operands, so they are syntax errors rather than paths.
    path_condition: $ => seq($._condition_operand, alias($.repetition_path, $.path)),

    presence_condition: $ => seq(
      $._condition_operand,
      alias($.repetition_path, $.path),
      token.immediate('?')
    ),

    group_condition: $ => seq('(', $._condition, ')'),

    // Keyed Repetition: `for item in collection key key { body }`. The body
    // is always braced and may be empty.
    keyed_repetition: $ => seq(
      'for',
      field('item', $.identifier),
      'in',
      field('collection', alias($.repetition_path, $.path)),
      'key',
      field('key', alias($.repetition_path, $.path)),
      field('body', $.element_body)
    ),

    // A bare dotted path with no whitespace around `.`.
    repetition_path: $ => seq(
      $.identifier,
      repeat(seq(
        token.immediate('.'),
        alias(token.immediate(IDENTIFIER), $.identifier)
      ))
    ),

    // Text and interpolation
    string_literal: $ => token(choice(
      seq('"', repeat(choice(/[^"\\{}#]/, /\\(?:[bfnrt\\"]|u[0-9a-fA-F]{4})/, /\{[\t ][^{}"]*\}/)), '"'),
      seq("`", repeat(/[^`\n\r]/), "`")
    )),

    interpolated_string: $ => prec.dynamic(1, seq(
      '"',
      repeat1(choice(
        $.interpolation_literal,
        $.literal_brace_object,
        $.interpolation
      )),
      '"'
    )),

    interpolation_literal: $ => /[^"\\{}]+/,

    literal_brace_object: $ => token(/\{[\t ][^{}"]*\}/),

    interpolation: $ => seq(
      '{',
      $.path,
      '}'
    ),

    path: $ => seq(
      $.identifier,
      repeat(seq('.', $.identifier))
    ),

    // Literals
    identifier: $ => IDENTIFIER,
    boolean_literal: $ => choice('true', 'false'),
    number_literal: $ => /\d+(\.\d+)?/,
    comment: $ => token(seq('//', /.*/))
  }
});

function commaSep1(rule) {
  return seq(rule, repeat(seq(',', rule)));
}

function commaSep1Trailing(rule) {
  return seq(rule, repeat(seq(',', rule)), optional(','));
}
