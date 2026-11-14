// External scanner for KTML line-sensitive element headers.
//
// KTML treats whitespace as insignificant except in one place: a newline ends
// an element's same-line header (its attributes and the opening token of its
// child). Tree-sitter's extras hide newlines from the grammar, so this scanner
// provides the zero-width `_same_line` token that the grammar places before
// every header continuation. It also recognizes the contextual `slot` keyword,
// guards condition operands against the literals `true` and `false`, and
// keeps a story Variant payload's `[` on its name's line.

#include "tree_sitter/parser.h"

#include <stdbool.h>
#include <string.h>

enum TokenType {
  SAME_LINE,
  SAME_LINE_VALUE,
  SLOT_KEYWORD,
  CONDITION_OPERAND,
  PAYLOAD_START,
  ERROR_SENTINEL,
};

void *tree_sitter_ktml_external_scanner_create(void) { return NULL; }
void tree_sitter_ktml_external_scanner_destroy(void *payload) {}
unsigned tree_sitter_ktml_external_scanner_serialize(void *payload, char *buffer) { return 0; }
void tree_sitter_ktml_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {}

static bool is_identifier_start(int32_t c) {
  return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_';
}

static bool is_identifier_char(int32_t c) {
  return is_identifier_start(c) || (c >= '0' && c <= '9') || c == '-';
}

// Characters that continue an attribute name past an identifier.
static bool is_name_continuation(int32_t c) { return c == ':' || c == '.' || c == '*'; }

static bool is_space(int32_t c) { return c == ' ' || c == '\t' || c == '\r'; }

// Reads an identifier into buffer (truncated) and returns its length.
static unsigned read_word(TSLexer *lexer, char *buffer, unsigned size) {
  unsigned length = 0;
  while (is_identifier_char(lexer->lookahead)) {
    if (length + 1 < size) {
      buffer[length] = (char)lexer->lookahead;
    }
    length++;
    lexer->advance(lexer, false);
  }
  buffer[length < size ? length : size - 1] = '\0';
  return length;
}

static void skip_spaces(TSLexer *lexer) {
  while (is_space(lexer->lookahead)) {
    lexer->advance(lexer, false);
  }
}

// Skips all whitespace, including newlines, but not comments.
static void skip_whitespace(TSLexer *lexer) {
  while (is_space(lexer->lookahead) || lexer->lookahead == '\n') {
    lexer->advance(lexer, false);
  }
}

// Called with the lexer at an identifier start and the token end already
// marked. Reports whether the header must end here: before `for item in`
// (a Keyed Repetition) or before `if` that is not an attribute named if.
static bool starts_sibling(TSLexer *lexer) {
  char word[8];
  unsigned length = read_word(lexer, word, sizeof word);
  if (is_name_continuation(lexer->lookahead)) {
    // `if:x` and `for.y` are attribute names.
    return false;
  }
  if (length == 2 && strcmp(word, "if") == 0) {
    skip_whitespace(lexer);
    return lexer->lookahead != '=';
  }
  if (length != 3 || strcmp(word, "for") != 0) {
    return false;
  }
  skip_whitespace(lexer);
  if (!is_identifier_start(lexer->lookahead)) {
    return false;
  }
  read_word(lexer, word, sizeof word);
  skip_whitespace(lexer);
  length = read_word(lexer, word, sizeof word);
  return length == 2 && strcmp(word, "in") == 0;
}

// spaced reports whether whitespace separates the header token before the
// lexer from the next one. An attribute name starting with @ or : needs it,
// so the deferred colon chain `a:href` is not an attribute.
static bool scan_same_line(TSLexer *lexer, bool spaced) {
  while (is_space(lexer->lookahead)) {
    lexer->advance(lexer, true);
    spaced = true;
  }
  lexer->mark_end(lexer);
  int32_t c = lexer->lookahead;
  if (is_identifier_start(c)) {
    return !starts_sibling(lexer);
  }
  if (c == '@' || c == ':' || c == '.') {
    // A spread, ...path, also needs the space.
    return spaced;
  }
  return c == '#' || c == '"' || c == '`' || c == '{' || c == '(' || (c >= '0' && c <= '9');
}

// slot is a keyword only when an identifier follows it on the same line and
// no attribute value follows that identifier.
static bool scan_slot_keyword(TSLexer *lexer) {
  while (is_space(lexer->lookahead) || lexer->lookahead == '\n') {
    lexer->advance(lexer, true);
  }
  char word[8];
  if (read_word(lexer, word, sizeof word) != 4 || strcmp(word, "slot") != 0) {
    return false;
  }
  lexer->mark_end(lexer);
  skip_spaces(lexer);
  if (!is_identifier_start(lexer->lookahead)) {
    return false;
  }
  read_word(lexer, word, sizeof word);
  if (is_name_continuation(lexer->lookahead)) {
    // `slot x:y="z"` is the HTML slot element with an attribute.
    return false;
  }
  skip_whitespace(lexer);
  int32_t c = lexer->lookahead;
  return c != '=' && c != '"' && c != '`' && !(c >= '0' && c <= '9');
}

// A condition path operand may not be the literal true or false.
static bool scan_condition_operand(TSLexer *lexer) {
  while (is_space(lexer->lookahead) || lexer->lookahead == '\n') {
    lexer->advance(lexer, true);
  }
  lexer->mark_end(lexer);
  if (!is_identifier_start(lexer->lookahead)) {
    return false;
  }
  char word[8];
  unsigned length = read_word(lexer, word, sizeof word);
  return !((length == 4 && strcmp(word, "true") == 0) || (length == 5 && strcmp(word, "false") == 0));
}

bool tree_sitter_ktml_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid_symbols) {
  if (valid_symbols[ERROR_SENTINEL]) {
    return false;
  }
  if (valid_symbols[PAYLOAD_START]) {
    // A Variant payload's `[` must be on the name's line.
    while (is_space(lexer->lookahead)) {
      lexer->advance(lexer, true);
    }
    if (lexer->lookahead == '[') {
      lexer->result_symbol = PAYLOAD_START;
      return true;
    }
    return false;
  }
  if (valid_symbols[SAME_LINE]) {
    bool spaced = false;
    while (is_space(lexer->lookahead)) {
      lexer->advance(lexer, true);
      spaced = true;
    }
    int32_t c = lexer->lookahead;
    if (valid_symbols[SAME_LINE_VALUE] && (c == '"' || c == '`' || (c >= '0' && c <= '9'))) {
      // A string or number directly after an attribute name on its line is
      // that attribute's shorthand value. A ``` raw-text literal is never a
      // value: it is the element's child.
      lexer->mark_end(lexer);
      lexer->result_symbol = SAME_LINE_VALUE;
      if (c == '`') {
        lexer->advance(lexer, false);
        if (lexer->lookahead == '`') {
          lexer->advance(lexer, false);
          if (lexer->lookahead == '`') {
            lexer->result_symbol = SAME_LINE;
          }
        }
      }
      return true;
    }
    if (c != '\n') {
      lexer->result_symbol = SAME_LINE;
      return scan_same_line(lexer, spaced);
    }
    // A newline ended the header. The next body node may be a slot fill.
  }
  if (valid_symbols[SLOT_KEYWORD]) {
    lexer->result_symbol = SLOT_KEYWORD;
    return scan_slot_keyword(lexer);
  }
  if (valid_symbols[CONDITION_OPERAND]) {
    lexer->result_symbol = CONDITION_OPERAND;
    return scan_condition_operand(lexer);
  }
  return false;
}
