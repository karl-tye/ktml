const assert = require("node:assert");
const { test } = require("node:test");
const fs = require("node:fs");
const path = require("node:path");

const Parser = require("tree-sitter");
const language = require(".");

test("can load grammar", () => {
  const parser = new Parser();
  assert.doesNotThrow(() => parser.setLanguage(language));
});

test("shared conformance corpus matches validity and recovery contract", async (t) => {
  const corpusDir = path.resolve(__dirname, "../../../testdata/conformance");
  const manifest = JSON.parse(
    fs.readFileSync(path.join(corpusDir, "manifest.json"), "utf8"),
  );
  assert.strictEqual(manifest.schemaVersion, 1);

  const parser = new Parser();
  parser.setLanguage(language);

  for (const fixture of manifest.cases) {
    await t.test(fixture.name, () => {
      const source = fs.readFileSync(path.join(corpusDir, fixture.source), "utf8");
      const tree = parser.parse(source);
      const recovery = collectRecovery(tree.rootNode);

      assert.strictEqual(tree.rootNode.hasError, !fixture.valid);
      assert.deepStrictEqual(recovery, fixture.treeSitter?.recovery ?? []);
      if (fixture.interpolations) {
        assert.deepStrictEqual(
          collectInterpolations(tree.rootNode, source),
          fixture.interpolations,
        );
      }
      if (fixture.segments) {
        assert.deepStrictEqual(
          collectSegments(tree.rootNode, source),
          fixture.segments,
        );
      }
      if (fixture.records) {
        assert.deepStrictEqual(
          collectRecords(tree.rootNode, source),
          fixture.records,
        );
      }
      if (fixture.states) {
        assert.deepStrictEqual(
          collectStates(tree.rootNode, source),
          fixture.states,
        );
      }
      if (fixture.stories) {
        assert.deepStrictEqual(collectStories(tree.rootNode, source), fixture.stories);
      }
      if (fixture.clauses) {
        assert.deepStrictEqual(
          collectClauses(tree.rootNode, source),
          fixture.clauses,
        );
      }
      if (fixture.imports) {
        assert.deepStrictEqual(
          collectImports(tree.rootNode, source),
          fixture.imports,
        );
      }
      if (fixture.repetitions) {
        assert.deepStrictEqual(
          collectRepetitions(tree.rootNode, source),
          fixture.repetitions,
        );
      }
      if (fixture.declarations) {
        assert.deepStrictEqual(
          collectDeclarations(tree.rootNode, source),
          fixture.declarations,
        );
      }
      if (fixture.attributeConditions) {
        assert.deepStrictEqual(
          collectAttributeConditions(tree.rootNode, source),
          fixture.attributeConditions,
        );
      }
      if (fixture.forwarding) {
        assert.deepStrictEqual(collectForwarding(tree.rootNode, source), fixture.forwarding);
      }
      if (fixture.markup) {
        assert.deepStrictEqual(collectMarkup(tree.rootNode, source), fixture.markup);
      }
      if (fixture.elements || fixture.conditions || fixture.slots) {
        const bodies = collectBodies(tree.rootNode, source);
        if (fixture.elements) {
          assert.deepStrictEqual(bodies.elements, fixture.elements);
        }
        if (fixture.conditions) {
          assert.deepStrictEqual(bodies.conditions, fixture.conditions);
        }
        if (fixture.slots) {
          assert.deepStrictEqual(bodies.slots, fixture.slots);
        }
      }
    });
  }
});

// Lists every body element, condition, and slot fill in source pre-order.
// Top-level elements are declarations in the Go parser, so only their
// bodies are walked. Element depth counts enclosing elements only.
function collectBodies(root, source) {
  const result = { elements: [], conditions: [], slots: [] };
  const walk = (node, depth, topLevel) => {
    switch (node.type) {
      case "element": {
        const name = node.childForFieldName("name");
        let childDepth = depth;
        if (!topLevel) {
          const group = node.childForFieldName("attributes");
          const attributes = (group ? group.namedChildren : node.namedChildren)
            .filter((child) => child.type === "attribute")
            .map((attribute) => normalizeAttribute(attribute, source));
          result.elements.push({
            name: text(name, source),
            nameStart: name.startIndex,
            nameEnd: name.endIndex,
            depth,
            multiline: group !== null,
            attributes,
          });
          childDepth = depth + 1;
        }
        const child = node.childForFieldName("child");
        if (child) {
          walk(child, childDepth, false);
        }
        return;
      }
      case "condition_node": {
        const keyword = node.children[0];
        const elseKeyword = node.children.find((child) => child.type === "else");
        const alternative = node.childForFieldName("alternative");
        const condition = {
          start: node.startIndex,
          end: node.endIndex,
          ifStart: keyword.startIndex,
          ifEnd: keyword.endIndex,
        };
        if (elseKeyword) {
          condition.elseStart = elseKeyword.startIndex;
          condition.elseEnd = elseKeyword.endIndex;
        }
        condition.elseIf = alternative?.type === "condition_node";
        condition.condition = normalizeCondition(node.childForFieldName("condition"), source);
        result.conditions.push(condition);
        walk(node.childForFieldName("consequence"), depth, false);
        if (alternative) {
          walk(alternative, depth, false);
        }
        return;
      }
      case "slot_fill": {
        const keyword = node.children[0];
        const name = node.childForFieldName("name");
        result.slots.push({
          name: text(name, source),
          start: node.startIndex,
          end: node.endIndex,
          keywordStart: keyword.startIndex,
          keywordEnd: keyword.endIndex,
          nameStart: name.startIndex,
          nameEnd: name.endIndex,
        });
        walk(node.childForFieldName("body"), depth, false);
        return;
      }
    }
    for (const child of node.namedChildren) {
      walk(child, depth, node.type === "source_file");
    }
  };
  walk(root, 0, false);
  return result;
}

// Lists every conditional attribute group in source pre-order. Branches list
// their named attributes; nested groups follow their parent.
function collectAttributeConditions(root, source) {
  const groups = [];
  const named = (block) =>
    block
      ? block.namedChildren
          .filter((child) => child.type === "attribute")
          .map((attribute) => normalizeAttribute(attribute, source))
      : [];
  const visitGroup = (node) => {
    const keyword = node.children[0];
    const elseKeyword = node.children.find((child) => child.type === "else");
    const consequence = node.childForFieldName("consequence");
    const alternative = node.childForFieldName("alternative");
    const group = {
      start: node.startIndex,
      end: node.endIndex,
      ifStart: keyword.startIndex,
      ifEnd: keyword.endIndex,
    };
    if (elseKeyword) {
      group.elseStart = elseKeyword.startIndex;
      group.elseEnd = elseKeyword.endIndex;
    }
    group.elseIf = alternative?.type === "attribute_condition";
    group.condition = normalizeCondition(node.childForFieldName("condition"), source);
    group.then = named(consequence);
    group.else = alternative?.type === "attribute_block" ? named(alternative) : [];
    groups.push(group);
    for (const child of consequence.namedChildren) {
      if (child.type === "attribute_condition") visitGroup(child);
    }
    if (alternative?.type === "attribute_condition") {
      visitGroup(alternative);
    } else if (alternative) {
      for (const child of alternative.namedChildren) {
        if (child.type === "attribute_condition") visitGroup(child);
      }
    }
  };
  const walk = (node) => {
    if (node.type === "attribute_condition") {
      visitGroup(node);
      return;
    }
    for (const child of node.namedChildren) {
      walk(child);
    }
  };
  walk(root);
  return groups;
}

// Lists spreads and attribute bag literals in source pre-order.
function collectForwarding(root, source) {
  const result = [];
  const walk = (node) => {
    if (node.type === "spread_attribute") {
      const path = node.namedChildren.find((child) => child.type === "path");
      result.push({ kind: "spread", start: node.startIndex, end: node.endIndex, path: normalizeRepetitionPath(path, source) });
      return;
    }
    if (node.type === "attribute") {
      const value = node.namedChildren.find((child) => child.type === "attribute_value");
      const bag = value?.namedChildren.find((child) => child.type === "attribute_bag");
      if (bag) {
        const name = node.namedChildren.find((child) => child.type === "attribute_name");
        const attributes = bag.namedChildren
          .filter((child) => child.type === "attribute")
          .map((attribute) => normalizeAttribute(attribute, source));
        const entry = { kind: "bag", start: node.startIndex, end: node.endIndex, name: text(name, source), bagStart: bag.startIndex, bagEnd: bag.endIndex };
        if (attributes.length > 0) entry.attributes = attributes;
        result.push(entry);
        for (const child of bag.namedChildren) walk(child);
        return;
      }
    }
    for (const child of node.namedChildren) {
      walk(child);
    }
  };
  walk(root);
  return result;
}

// Lists every doctype and HTML comment in source pre-order.
function collectMarkup(root, source) {
  const markup = [];
  const walk = (node) => {
    if (node.type === "doctype") {
      const name = node.childForFieldName("name");
      markup.push({ kind: "doctype", start: node.startIndex, end: node.endIndex, name: text(name, source) });
      return;
    }
    if (node.type === "html_comment") {
      const raw = text(node, source);
      markup.push({ kind: "comment", start: node.startIndex, end: node.endIndex, text: raw.slice(4, -3) });
      return;
    }
    if (node.type === "raw_text") {
      const raw = text(node, source);
      markup.push({ kind: "raw-text", start: node.startIndex, end: node.endIndex, text: raw.slice(3, -3) });
      return;
    }
    for (const child of node.namedChildren) {
      walk(child);
    }
  };
  walk(root);
  return markup;
}

// An attribute is named by its attribute_name, or "id" for #name, and keeps its
// whole range.
function normalizeAttribute(attribute, source) {
  const id = attribute.namedChildren.find((child) => child.type === "element_id");
  const name = attribute.namedChildren.find((child) => child.type === "attribute_name");
  return {
    name: id ? "id" : text(name, source),
    start: attribute.startIndex,
    end: attribute.endIndex,
  };
}

const conditionKinds = {
  path_condition: "path",
  presence_condition: "presence",
  not_condition: "not",
  group_condition: "group",
  and_condition: "and",
  or_condition: "or",
};

// Normalizes the binary and/or chains Tree-sitter builds into the Go parser's
// n-ary operand lists. Groups keep their own node, so only direct chains
// flatten.
function normalizeCondition(node, source) {
  const kind = conditionKinds[node.type];
  const result = { kind, start: node.startIndex, end: node.endIndex };
  if (kind === "path" || kind === "presence") {
    const path = node.namedChildren.find((child) => child.type === "path");
    result.path = normalizeRepetitionPath(path, source);
    return result;
  }
  if (kind === "and" || kind === "or") {
    const operands = [];
    const flatten = (operand) => {
      if (operand.type === node.type) {
        flatten(operand.childForFieldName("left"));
        flatten(operand.childForFieldName("right"));
      } else {
        operands.push(normalizeCondition(operand, source));
      }
    };
    flatten(node);
    result.operands = operands;
    return result;
  }
  const operand = node.namedChildren.find((child) => child.type !== "comment");
  result.operands = [normalizeCondition(operand, source)];
  return result;
}

function collectRecovery(node) {
  const recovery = [];
  if (node.type === "ERROR" || node.isMissing) {
    recovery.push({
      kind: node.isMissing ? "missing" : "error",
      start: node.startIndex,
      end: node.endIndex,
    });
    return recovery;
  }
  for (const child of node.children) {
    recovery.push(...collectRecovery(child));
  }
  return recovery;
}

// Lists every Keyed Repetition in source order, depth-first, so nested
// repetitions follow their parent.
function collectRepetitions(node, source) {
  const repetitions = [];
  if (node.type === "keyed_repetition") {
    const keyword = node.children[0];
    const item = node.childForFieldName("item");
    const inKeyword = node.children.find((child) => child.type === "in");
    const keyKeyword = node.children.find((child) => child.type === "key");
    repetitions.push({
      item: text(item, source),
      start: node.startIndex,
      end: node.endIndex,
      keywordStart: keyword.startIndex,
      keywordEnd: keyword.endIndex,
      itemStart: item.startIndex,
      itemEnd: item.endIndex,
      inStart: inKeyword.startIndex,
      inEnd: inKeyword.endIndex,
      collection: normalizeRepetitionPath(node.childForFieldName("collection"), source),
      keyKeywordStart: keyKeyword.startIndex,
      keyKeywordEnd: keyKeyword.endIndex,
      key: normalizeRepetitionPath(node.childForFieldName("key"), source),
    });
  }
  for (const child of node.namedChildren) {
    repetitions.push(...collectRepetitions(child, source));
  }
  return repetitions;
}

function normalizeRepetitionPath(pathNode, source) {
  const parts = pathNode.namedChildren
    .filter((child) => child.type === "identifier")
    .map((part) => ({
      name: text(part, source),
      start: part.startIndex,
      end: part.endIndex,
    }));
  return {
    name: text(pathNode, source),
    start: pathNode.startIndex,
    end: pathNode.endIndex,
    parts,
  };
}

function collectRecords(root, source) {
  return root.namedChildren
    .filter((node) => node.type === "record_declaration")
    .map((record) => {
      const name = record.childForFieldName("name");
      return {
        name: source.slice(name.startIndex, name.endIndex),
        start: name.startIndex,
        end: name.endIndex,
        fields: record.namedChildren
          .filter((node) => node.type === "field_declaration")
          .map((field) => normalizeField(field, source)),
      };
    });
}

// Lists top-level story and fixture declarations in source order. Empty
// argument, field, and item lists are omitted, as in the Go normalization.
function collectStories(root, source) {
  return root.namedChildren
    .filter((node) => node.type === "story_declaration" || node.type === "fixture_declaration")
    .map((declaration) => {
      const keyword = declaration.children[0];
      const name = declaration.childForFieldName("name");
      const result = {
        kind: declaration.type === "story_declaration" ? "story" : "fixture",
        name: text(name, source),
        start: declaration.startIndex,
        end: declaration.endIndex,
        keywordStart: keyword.startIndex,
        keywordEnd: keyword.endIndex,
        nameStart: name.startIndex,
        nameEnd: name.endIndex,
      };
      if (result.kind === "fixture") {
        const type = declaration.childForFieldName("type");
        result.type = text(type, source);
        result.typeStart = type.startIndex;
        result.typeEnd = type.endIndex;
        result.value = normalizeStoryValue(declaration.childForFieldName("value"), source);
        return result;
      }
      const component = declaration.childForFieldName("component");
      result.component = text(component, source);
      result.componentStart = component.startIndex;
      result.componentEnd = component.endIndex;
      const argumentList = declaration.childForFieldName("arguments");
      if (argumentList) {
        result.argumentsStart = argumentList.startIndex;
        result.argumentsEnd = argumentList.endIndex;
        assignNonEmpty(result, "arguments", normalizeValueArguments(argumentList, source));
      }
      const body = declaration.childForFieldName("body");
      if (body) {
        result.bodyStart = body.startIndex;
        result.bodyEnd = body.endIndex;
      }
      return result;
    });
}

function normalizeValueArguments(list, source) {
  return list.namedChildren
    .filter((node) => node.type === "value_argument")
    .map((argument) => {
      const name = argument.childForFieldName("name");
      return {
        name: text(name, source),
        start: argument.startIndex,
        end: argument.endIndex,
        nameStart: name.startIndex,
        nameEnd: name.endIndex,
        value: normalizeStoryValue(argument.childForFieldName("value"), source),
      };
    });
}

const SCALAR_LITERALS = {
  string_literal: "string",
  number_literal: "number",
  boolean_literal: "boolean",
};

function normalizeStoryValue(value, source) {
  const result = { start: value.startIndex, end: value.endIndex };
  if (SCALAR_LITERALS[value.type]) {
    return { kind: "scalar", ...result, literal: SCALAR_LITERALS[value.type], raw: text(value, source) };
  }
  switch (value.type) {
    case "value_name": {
      const named = { kind: "name", ...result, name: text(value.childForFieldName("name"), source) };
      const payload = value.childForFieldName("payload");
      if (payload) {
        named.payloadStart = payload.startIndex;
        named.payloadEnd = payload.endIndex;
        assignNonEmpty(named, "fields", normalizeValueArguments(payload, source));
      }
      return named;
    }
    case "record_value":
      return { kind: "fields", ...result, fields: normalizeValueArguments(value, source) };
    case "list_value": {
      const items = value.namedChildren
        .filter((node) => node.type !== "comment")
        .map((item) => normalizeStoryValue(item, source));
      return items.length === 0 ? { kind: "empty", ...result } : { kind: "list", ...result, items };
    }
  }
  throw new Error(`unexpected story value ${value.type}`);
}

function assignNonEmpty(target, key, list) {
  if (list.length > 0) {
    target[key] = list;
  }
}

function collectStates(root, source) {
  return root.namedChildren
    .filter((node) => node.type === "state_declaration")
    .map((state) => {
      const keyword = state.children[0];
      const name = state.childForFieldName("name");
      return {
        name: text(name, source),
        start: state.startIndex,
        end: state.endIndex,
        keywordStart: keyword.startIndex,
        keywordEnd: keyword.endIndex,
        nameStart: name.startIndex,
        nameEnd: name.endIndex,
        variants: state.namedChildren
          .filter((node) => node.type === "variant_declaration")
          .map((variant) => normalizeVariant(variant, source)),
      };
    });
}

function normalizeVariant(variant, source) {
  const name = variant.childForFieldName("name");
  const payload = variant.childForFieldName("payload");
  const result = {
    name: text(name, source),
    start: variant.startIndex,
    end: variant.endIndex,
    nameStart: name.startIndex,
    nameEnd: name.endIndex,
    payload: payload !== null,
  };
  if (payload) {
    result.payloadStart = payload.startIndex;
    result.payloadEnd = payload.endIndex;
  }
  result.fields = (payload?.namedChildren ?? [])
    .filter((node) => node.type === "field_declaration")
    .map((field) => normalizeField(field, source));
  return result;
}

function collectClauses(root, source) {
  return root.namedChildren
    .filter((node) => node.type === "render_clause")
    .map((clause) => {
      const name = clause.childForFieldName("name");
      const selector = clause.childForFieldName("selector");
      return {
        name: text(name, source),
        start: clause.startIndex,
        end: clause.endIndex,
        nameStart: name.startIndex,
        nameEnd: name.endIndex,
        selector: text(selector, source),
        selectorStart: selector.startIndex,
        selectorEnd: selector.endIndex,
        pattern: normalizePattern(clause.childForFieldName("pattern"), source),
        bindings: clause
          .childrenForFieldName("binding")
          .map((binding) => normalizeBinding(binding, source)),
      };
    });
}

function normalizePattern(pattern, source) {
  const name = pattern.childForFieldName("name");
  const qualifier = pattern.childForFieldName("qualifier");
  const result = {
    name: text(name, source),
    start: pattern.startIndex,
    end: pattern.endIndex,
    nameStart: name.startIndex,
    nameEnd: name.endIndex,
  };
  if (qualifier) {
    result.qualifier = text(qualifier, source);
    result.qualifierStart = qualifier.startIndex;
    result.qualifierEnd = qualifier.endIndex;
  }
  return result;
}

function normalizeBinding(binding, source) {
  const field = binding.childForFieldName("field");
  const alias = binding.childForFieldName("alias");
  const result = {
    field: text(field, source),
    start: binding.startIndex,
    end: binding.endIndex,
    fieldStart: field.startIndex,
    fieldEnd: field.endIndex,
  };
  if (alias) {
    result.alias = text(alias, source);
    result.aliasStart = alias.startIndex;
    result.aliasEnd = alias.endIndex;
  }
  return result;
}

function text(node, source) {
  return source.slice(node.startIndex, node.endIndex);
}

function normalizeField(field, source) {
  const name = field.childForFieldName("name");
  const type = field.childForFieldName("type");
  const optional = field.children.find((node) => node.type === "?");
  const result = {
    name: source.slice(name.startIndex, name.endIndex),
    start: field.startIndex,
    end: field.endIndex,
    nameStart: name.startIndex,
    nameEnd: name.endIndex,
    type: source.slice(type.startIndex, type.endIndex),
    typeStart: type.startIndex,
    typeEnd: type.endIndex,
    optional: optional !== undefined,
  };
  if (optional) {
    result.optionalStart = optional.startIndex;
    result.optionalEnd = optional.endIndex;
  }
  return result;
}

function collectImports(root, source) {
  return root.namedChildren
    .filter((node) => node.type === "import_statement")
    .map((statement) => {
      const pathNode = statement.namedChildren.find((node) => node.type === "string_literal");
      return {
        path: source.slice(pathNode.startIndex, pathNode.endIndex),
        pathStart: pathNode.startIndex,
        pathEnd: pathNode.endIndex,
        specifiers: statement.namedChildren
          .filter((node) => node.type === "identifier" || node.type === "import_alias")
          .map((node) => normalizeSpecifier(node, source)),
      };
    });
}

function normalizeSpecifier(node, source) {
  const [name, alias] = node.type === "import_alias" ? node.namedChildren : [node];
  const result = {
    name: source.slice(name.startIndex, name.endIndex),
    start: name.startIndex,
    end: name.endIndex,
  };
  if (alias) {
    result.alias = source.slice(alias.startIndex, alias.endIndex);
    result.aliasStart = alias.startIndex;
    result.aliasEnd = alias.endIndex;
  }
  return result;
}

function collectDeclarations(root, source) {
  return root.namedChildren
    .filter((node) => node.type === "component_definition")
    .map((component) => {
      const name = component.namedChildren.find((node) => node.type === "identifier");
      const parameters = component.namedChildren.find((node) => node.type === "parameter_list");
      const body = component.namedChildren.find((node) => node.type === "element_body");
      const defaultKeyword = component.childForFieldName("default");
      const result = {
        name: source.slice(name.startIndex, name.endIndex),
        start: name.startIndex,
        end: name.endIndex,
        bodyless: body === undefined && defaultKeyword === null,
      };
      if (defaultKeyword) {
        result.defaultStart = defaultKeyword.startIndex;
        result.defaultEnd = defaultKeyword.endIndex;
      }
      result.inputs = parameters.namedChildren
          .filter((node) => node.type === "parameter")
          .map((parameter) => normalizeParameter(parameter, source));
      return result;
    });
}

function normalizeParameter(parameter, source) {
  const name = parameter.namedChildren.find((node) => node.type === "identifier");
  const type = parameter.namedChildren.find((node) => node.type === "type");
  const defaultValue = parameter.namedChildren.find((node) => node.type === "default_value");
  const text = source.slice(parameter.startIndex, parameter.endIndex);
  const questionOffset = text.indexOf("?");
  const assignOffset = text.indexOf("=");
  const result = {
    name: source.slice(name.startIndex, name.endIndex),
    start: parameter.startIndex,
    end: parameter.endIndex,
    nameStart: name.startIndex,
    nameEnd: name.endIndex,
    type: source.slice(type.startIndex, type.endIndex),
    typeStart: type.startIndex,
    typeEnd: type.endIndex,
    mode: questionOffset >= 0 ? "optional" : assignOffset >= 0 ? "defaulted" : "required",
  };
  if (questionOffset >= 0) {
    result.optionalStart = parameter.startIndex + questionOffset;
    result.optionalEnd = result.optionalStart + 1;
  }
  if (assignOffset >= 0) {
    result.operatorStart = parameter.startIndex + assignOffset;
    result.operatorEnd = result.operatorStart + 1;
    const literal = defaultValue.namedChildren[0];
    result.default = {
      kind: literal.type.replace("_literal", ""),
      raw: source.slice(literal.startIndex, literal.endIndex),
      start: literal.startIndex,
      end: literal.endIndex,
    };
    if (literal.type === "string_literal") {
      result.default.string = decodeStringLiteral(result.default.raw);
    } else if (literal.type === "boolean_literal") {
      result.default.boolean = result.default.raw === "true";
    } else if (literal.type === "number_literal") {
      result.default.number = Number(result.default.raw);
    }
  }
  return result;
}

function decodeStringLiteral(raw) {
  if (raw.startsWith("`")) {
    return raw.slice(1, -1);
  }
  return JSON.parse(raw);
}

function collectSegments(node, source) {
  const segments = [];
  if (node.type === "interpolation_literal" || node.type === "literal_brace_object") {
    segments.push({
      kind: "literal",
      start: node.startIndex,
      end: node.endIndex,
      literal: source.slice(node.startIndex, node.endIndex),
    });
    return segments;
  }
  if (node.type === "interpolation") {
    const pathNode = node.namedChildren.find((child) => child.type === "path");
    segments.push({
      kind: "path",
      start: node.startIndex,
      end: node.endIndex,
      path: source.slice(pathNode.startIndex, pathNode.endIndex),
    });
    return segments;
  }
  for (const child of node.namedChildren) {
    segments.push(...collectSegments(child, source));
  }
  return coalesceLiteralSegments(segments);
}

function coalesceLiteralSegments(segments) {
  const normalized = [];
  for (const segment of segments) {
    const previous = normalized.at(-1);
    if (previous?.kind === "literal" && segment.kind === "literal" && previous.end === segment.start) {
      previous.end = segment.end;
      previous.literal += segment.literal;
    } else {
      normalized.push(segment);
    }
  }
  return normalized;
}

function collectInterpolations(node, source) {
  const interpolations = [];
  if (node.type === "interpolation") {
    const pathNode = node.namedChildren.find((child) => child.type === "path");
    const parts = pathNode.namedChildren
      .filter((child) => child.type === "identifier")
      .map((part) => ({
        name: source.slice(part.startIndex, part.endIndex),
        start: part.startIndex,
        end: part.endIndex,
      }));
    interpolations.push({
      start: node.startIndex,
      end: node.endIndex,
      path: parts.map((part) => part.name).join("."),
      parts,
    });
    return interpolations;
  }
  for (const child of node.children) {
    interpolations.push(...collectInterpolations(child, source));
  }
  return interpolations;
}
