import process from 'node:process';
import console from 'node:console';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import ts from 'typescript';
import postcss from 'postcss';

const namedColors = new Set(('aliceblue antiquewhite aqua aquamarine azure beige bisque black blanchedalmond blue blueviolet brown burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen darkgrey darkkhaki darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon darkseagreen darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink deepskyblue dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro ghostwhite gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo ivory khaki lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan lightgoldenrodyellow lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime limegreen linen magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream mistyrose moccasin navajowhite navy oldlace olive olivedrab orange orangered orchid palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru pink plum powderblue purple rebeccapurple red rosybrown royalblue saddlebrown salmon sandybrown seagreen seashell sienna silver skyblue slateblue slategray slategrey snow springgreen steelblue tan teal thistle tomato turquoise violet wheat white whitesmoke yellow yellowgreen').split(' '));
const colorProperty = /^(?:color|background(?:-color|-image)?|border(?:-(?:top|right|bottom|left|block|inline))?(?:-color|-image|-image-source)?|mask(?:-image)?|outline(?:-color)?|fill|stroke|stop-color|flood-color|lighting-color|text-decoration(?:-color)?|box-shadow|text-shadow|caret-color|accent-color)$/i;
const paletteClass = /^(?:bg|text|border|outline|ring|fill|stroke|decoration|shadow|from|via|to|accent|caret|divide|placeholder)-(?:white|black|slate|gray|grey|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)(?:$|[-/])/i;
const colorUtility = /^(?:bg|text|border|outline|ring|fill|stroke|decoration|shadow|from|via|to|accent|caret|divide|placeholder)-\[(.*)\]$/i;

function hasColor(value, property = '') {
  // Ignore token references themselves, but still inspect any fallback value.
  const withoutVars = value.replace(/var\(\s*--[\w-]+\s*(?:,\s*([^()]*))?\)/gi, '$1');
  if (/#[\da-f]{3,8}\b|\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color|color-mix)\s*\(/i.test(withoutVars)) return true;
  if (!colorProperty.test(property)) return false;
  return (withoutVars.match(/[a-z]+/gi) ?? []).some((word) => namedColors.has(word.toLowerCase()));
}

function classViolations(value) {
  return value.split(/\s+/).filter((token) => {
    // Colons in arbitrary values are not variant separators.
    const utility = token.split(/:(?![^[]*\])/).at(-1).replace(/^!/, '');
    if (paletteClass.test(utility)) return true;
    const arbitrary = utility.match(colorUtility);
    return arbitrary && hasColor(arbitrary[1].replace(/^color:/i, ''), 'color');
  });
}

function propertyName(name) {
  return name.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`);
}

// Only inspect color-bearing syntax, not arbitrary strings such as translations.
export function violations(text, filename = 'fixture.tsx') {
  const found = [];
  if (filename.endsWith('.css')) {
    const root = postcss.parse(text, { from: filename });
    root.walkDecls((decl) => { if (hasColor(decl.value, decl.prop)) found.push(`${decl.prop}: ${decl.value}`); });
    return found;
  }
  const source = ts.createSourceFile(filename, text, ts.ScriptTarget.Latest, true, filename.endsWith('.jsx') ? ts.ScriptKind.JSX : ts.ScriptKind.TSX);
  // Resolve only local const bindings, respecting enclosing blocks and parameters.
  function resolve(identifier) {
    const name = identifier.text;
    for (let scope = identifier.parent; scope; scope = scope.parent) {
      if (ts.isFunctionLike(scope) && scope.parameters.some((p) => ts.isIdentifier(p.name) && p.name.text === name)) return;
      if (!ts.isSourceFile(scope) && !ts.isBlock(scope)) continue;
      for (const statement of scope.statements) {
        if (!ts.isVariableStatement(statement)) continue;
        for (const declaration of statement.declarationList.declarations) {
          if (ts.isIdentifier(declaration.name) && declaration.name.text === name) {
            return statement.declarationList.flags & ts.NodeFlags.Const ? declaration.initializer : undefined;
          }
        }
      }
    }
  }
  function inspectExpression(node, callback, seen = new Set()) {
    if (!node) return;
    if (ts.isIdentifier(node)) {
      const binding = resolve(node);
      if (binding && !seen.has(binding)) {
        seen.add(binding);
        inspectExpression(binding, callback, seen);
        seen.delete(binding);
      }
    } else if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) callback(node.text);
    else if (ts.isTemplateExpression(node)) {
      callback(node.head.text);
      for (const span of node.templateSpans) { inspectExpression(span.expression, callback, seen); callback(span.literal.text); }
    } else ts.forEachChild(node, (child) => inspectExpression(child, callback, seen));
  }
  function inspectObject(node, kind, seen = new Set()) {
    if (!node) return;
    if (ts.isJsxExpression(node) || ts.isParenthesizedExpression(node) || ts.isAsExpression(node) || ts.isSatisfiesExpression(node) || ts.isNonNullExpression(node)) {
      inspectObject(node.expression, kind, seen);
    } else if (ts.isIdentifier(node)) {
      const binding = resolve(node);
      if (binding && !seen.has(binding)) {
        seen.add(binding);
        inspectObject(binding, kind, seen);
        seen.delete(binding);
      }
    } else if (ts.isObjectLiteralExpression(node)) {
      for (const property of node.properties) {
        if (ts.isSpreadAssignment(property)) inspectObject(property.expression, kind, seen);
        else if (ts.isPropertyAssignment(property) || ts.isShorthandPropertyAssignment(property)) {
          if (!ts.isIdentifier(property.name) && !ts.isStringLiteral(property.name)) continue;
          const name = property.name.text;
          const value = ts.isPropertyAssignment(property) ? property.initializer : property.name;
          if (kind === 'props' && name === 'style') inspectObject(value, 'style', seen);
          else if (kind === 'props' && (name === 'className' || name === 'class')) inspectExpression(value, (text) => found.push(...classViolations(text)));
          else if (colorProperty.test(propertyName(name))) inspectExpression(value, (text) => { if (hasColor(text, propertyName(name))) found.push(`${name}: ${text}`); });
        }
      }
    }
  }
  function visit(node) {
    if (ts.isJsxAttribute(node) && node.initializer) {
      const name = node.name.getText(source);
      if (name === 'className' || name === 'class') inspectExpression(node.initializer, (value) => found.push(...classViolations(value)));
      else if (name === 'style') inspectObject(node.initializer, 'style');
      else if (colorProperty.test(propertyName(name))) inspectExpression(node.initializer, (value) => { if (hasColor(value, propertyName(name))) found.push(`${name}: ${value}`); });
    } else if (ts.isJsxSpreadAttribute(node)) inspectObject(node.expression, 'props');
    ts.forEachChild(node, visit);
  }
  visit(source);
  return found;
}

function check(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'theme' || entry.name === 'i18n' || entry.name.includes('.test.')) continue;
    const path = join(dir, entry.name);
    if (entry.isDirectory()) check(path);
    else if (/\.(css|tsx?|jsx?)$/.test(entry.name)) {
      const found = violations(readFileSync(path, 'utf8'), path);
      if (found.length) { console.error(`${path}: forbidden colors: ${found.join(', ')}`); process.exitCode = 1; }
    }
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) check('src');
