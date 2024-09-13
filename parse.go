// Copyright 2016 José Santos <henrique_1609@me.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package jet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/oarkflow/jet/utils/e"
)

func unquote(text string) (string, error) {
	return strconv.Unquote(text)
}

// Template is the representation of a single parsed template.
type Template struct {
	Name      string // name of the template represented by the tree.
	ParseName string // name of the top-level template during parsing, for error messages.

	set     *Set
	extends *Template
	imports []*Template

	processedBlocks map[string]*BlockNode
	passedBlocks    map[string]*BlockNode
	Root            *ListNode // top-level root of the tree.
	placeholders    []string
	text            string // text parsed to create the template (or its parent)

	// Parsing only; cleared after parse.
	lex       *lexer
	token     [3]item // three-token lookahead for parser.
	peekCount int
}

func (t *Template) Placeholders() []string {
	return t.placeholders
}

func (t *Template) ParseMap(data any, asMap ...bool) (result string, err error) {
	var d bytes.Buffer
	var bt []byte
	switch data := data.(type) {
	case map[string]any:
		if len(asMap) > 0 && asMap[0] {
			err = t.Execute(&d, nil, data)
			if err != nil {
				return
			}
			result = d.String()
		} else {
			varMap := make(VarMap)
			for k, v := range data {
				varMap.Set(k, v)
			}
			err = t.Execute(&d, varMap, nil)
			if err != nil {
				return
			}
			result = d.String()
		}
		return
	case VarMap:
		err = t.Execute(&d, data, nil)
		if err != nil {
			return
		}
		result = d.String()
		return
	default:
		bt, err = json.Marshal(data)
		if err != nil {
			return
		}
		var d map[string]any
		err = json.Unmarshal(bt, &d)
		if err != nil {
			return
		}
		return t.ParseMap(d)
	}
}

func (t *Template) String() (template string) {
	if t.extends != nil {
		if len(t.Root.Nodes) > 0 && len(t.imports) == 0 {
			template += fmt.Sprintf("{{extends %q}}", t.extends.ParseName)
		} else {
			template += fmt.Sprintf("{{extends %q}}", t.extends.ParseName)
		}
	}

	for k, _import := range t.imports {
		if t.extends == nil && k == 0 {
			template += fmt.Sprintf("{{import %q}}", _import.ParseName)
		} else {
			template += fmt.Sprintf("\n{{import %q}}", _import.ParseName)
		}
	}

	if t.extends != nil || len(t.imports) > 0 {
		if len(t.Root.Nodes) > 0 {
			template += "\n" + t.Root.String()
		}
	} else {
		template += t.Root.String()
	}
	return
}

func (t *Template) addBlocks(blocks map[string]*BlockNode) {
	if len(blocks) == 0 {
		return
	}
	if t.processedBlocks == nil {
		t.processedBlocks = make(map[string]*BlockNode)
	}
	for key, value := range blocks {
		t.processedBlocks[key] = value
	}
}

// next returns the next token.
func (t *Template) next() item {
	if t.peekCount > 0 {
		t.peekCount--
	} else {
		t.token[0] = t.lex.nextItem()
	}
	return t.token[t.peekCount]
}

// backup backs the input stream up one token.
func (t *Template) backup() {
	t.peekCount++
}

// backup2 backs the input stream up two tokens.
// The zeroth token is already there.
func (t *Template) backup2(t1 item) {
	t.token[1] = t1
	t.peekCount = 2
}

// backup3 backs the input stream up three tokens
// The zeroth token is already there.
func (t *Template) backup3(t2, t1 item) {
	// Reverse order: we're pushing back.
	t.token[1] = t1
	t.token[2] = t2
	t.peekCount = 3
}

// peek returns but does not consume the next token.
func (t *Template) peek() item {
	if t.peekCount > 0 {
		return t.token[t.peekCount-1]
	}
	t.peekCount = 1
	t.token[0] = t.lex.nextItem()
	return t.token[0]
}

// nextNonSpace returns the next non-space token.
func (t *Template) nextNonSpace() (token item) {
	for {
		token = t.next()
		if token.typ != itemSpace {
			break
		}
	}
	return token
}

// peekNonSpace returns but does not consume the next non-space token.
func (t *Template) peekNonSpace() (token item) {
	for {
		token = t.next()
		if token.typ != itemSpace {
			break
		}
	}
	t.backup()
	return token
}

// errorf formats the error and terminates processing.
func (t *Template) error(reason, message string) e.Error {
	t.Root = nil
	if reason == "" {
		reason = e.TemplateErrorReason
	}
	return e.Build(
		reason,
		t.ParseName,
		message,
		&e.Position{L: t.lex.lineNumber(), C: 0},
	)
}

// expect consumes the next token and guarantees it has the required type.
func (t *Template) expect(expectedType itemType, context, expected string) e.Error {
	token := t.nextNonSpace()
	if token.typ != expectedType {
		return t.unexpected(token, context, expected)
	}
	return nil
}

// expectI consumes the next token and guarantees it has the required type.
func (t *Template) expectI(expectedType itemType, context, expected string) (item, e.Error) {
	token := t.nextNonSpace()
	if token.typ != expectedType {
		return item{}, t.unexpected(token, context, expected)
	}
	return token, nil
}

func (t *Template) expectRightDelim(context string) e.Error {
	return t.expect(itemRightDelim, context, "closing delimiter")
}

func (t *Template) expectRightDelimI(context string) (item, e.Error) {
	return t.expectI(itemRightDelim, context, "closing delimiter")
}

// expectOneOf consumes the next token and guarantees it has one of the required types.
func (t *Template) expectOneOf(expected1, expected2 itemType, context, expectedAs string) (item, e.Error) {
	token := t.nextNonSpace()
	if token.typ != expected1 && token.typ != expected2 {
		return item{}, t.unexpected(token, context, expectedAs)
	}
	return token, nil
}

// unexpected complains about the token and terminates processing.
func (t *Template) unexpected(token item, context, expected string) e.Error {
	switch {
	case token.typ == itemImport,
		token.typ == itemExtends:
		return t.error(e.UnexpectedKeywordReason, fmt.Sprintf("parsing %s: unexpected keyword '%s' ('%s' statements must be at the beginning of the template)", context, token.val, token.val))
	case token.typ > itemKeyword:
		return t.error(e.UnexpectedKeywordReason, fmt.Sprintf("parsing %s: unexpected keyword '%s' (expected %s)", context, token.val, expected))
	default:
		return t.error(e.UnexpectedTokenReason, fmt.Sprintf("parsing %s: unexpected token '%s' (expected %s)", context, token.val, expected))
	}
}

// recover is the handler that turns panics into returns from the top level of Parse.
func (t *Template) recover(errp *error) {
	err := recover()
	if err != nil {
		if _, ok := err.(runtime.Error); ok {
			panic(err)
		}
		if t != nil {
			t.lex.drain()
			t.stopParse()
		}
		*errp = err.(error)
	}
	return
}

func (s *Set) parse(name, text string, cacheAfterParsing bool) (t *Template, err e.Error) {
	var placeholders []string
	matches := s.placeholderParser.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		placeholders = append(placeholders, strings.TrimSpace(match[1]))
	}
	t = &Template{
		Name:         name,
		ParseName:    name,
		text:         text,
		set:          s,
		placeholders: placeholders,
		passedBlocks: make(map[string]*BlockNode),
	}

	lexer := newLexer(name, text, false)
	lexer.setDelimiters(s.leftDelim, s.rightDelim)
	lexer.lex()
	t.startParse(lexer)
	if _, err = t.parseTemplate(cacheAfterParsing); err != nil {
		return nil, err
	}
	t.stopParse()

	if t.extends != nil {
		t.addBlocks(t.extends.processedBlocks)
	}

	for _, _import := range t.imports {
		t.addBlocks(_import.processedBlocks)
	}

	t.addBlocks(t.passedBlocks)

	return t, err
}

func (t *Template) expectString(context string) (string, e.Error) {
	token, err := t.expectOneOf(itemString, itemRawString, context, "string literal")
	if err != nil {
		return "", err
	}
	s, unquoteErr := unquote(token.val)
	if err != nil {
		return "", t.error("", unquoteErr.Error())
	}
	return s, nil
}

// parse is the top-level parser for a template, essentially the same
// It runs to EOF.
func (t *Template) parseTemplate(cacheAfterParsing bool) (next Node, err e.Error) {
	t.Root = t.newList(t.peek().pos)
	// {{ extends|import stringLiteral }}
	for t.peek().typ != itemEOF {
		delim := t.next()
		if delim.typ == itemText && strings.TrimSpace(delim.val) == "" {
			continue // skips empty text nodes
		}
		if delim.typ == itemLeftDelim {
			token := t.nextNonSpace()
			if token.typ == itemExtends || token.typ == itemImport {
				s, err := t.expectString("extends|import")
				if err != nil {
					return nil, err
				}
				if token.typ == itemExtends {
					if t.extends != nil {
						return nil, t.error(e.UnexpectedClauseReason, "Unexpected extends clause: each template can only extend one template")
					} else if len(t.imports) > 0 {
						return nil, t.error(e.UnexpectedClauseReason, "Unexpected extends clause: the 'extends' clause should come before all import clauses")
					}
					var err error
					t.extends, err = t.set.getSiblingTemplate(s, t.Name, cacheAfterParsing)
					if err != nil {
						return nil, t.error("", err.Error())
					}
				} else {
					tt, err := t.set.getSiblingTemplate(s, t.Name, cacheAfterParsing)
					if err != nil {
						return nil, t.error("", err.Error())
					}
					t.imports = append(t.imports, tt)
				}
				if err = t.expect(itemRightDelim, "extends|import", "closing delimiter"); err != nil {
					return nil, err
				}
			} else {
				t.backup2(delim)
				break
			}
		} else {
			t.backup()
			break
		}
	}

	for t.peek().typ != itemEOF {
		n, err := t.textOrAction()
		if err != nil {
			return nil, err
		}
		switch n.Type() {
		case nodeEnd, nodeElse, nodeContent:
			return nil, t.error(e.UnexpectedReason, fmt.Sprintf("unexpected %s", n))
		default:
			t.Root.append(n)
		}
	}
	return nil, nil
}

// startParse initializes the parser, using the lexer.
func (t *Template) startParse(lex *lexer) {
	t.Root = nil
	t.lex = lex
}

// stopParse terminates parsing.
func (t *Template) stopParse() {
	t.lex = nil
}

// IsEmptyTree reports whether this tree (node) is empty of everything but space.
func IsEmptyTree(n Node) bool {
	switch n := n.(type) {
	case nil:
		return true
	case *ActionNode:
	case *IfNode:
	case *ListNode:
		for _, node := range n.Nodes {
			if !IsEmptyTree(node) {
				return false
			}
		}
		return true
	case *RangeNode:
	case *IncludeNode:
	case *TextNode:
		return len(bytes.TrimSpace(n.Text)) == 0
	case *BlockNode:
	case *YieldNode:
	default:
		panic("unknown node: " + n.String())
	}
	return false
}

func (t *Template) blockParametersList(isDeclaring bool, context string) (*BlockParameterList, e.Error) {
	block := &BlockParameterList{}

	if err := t.expect(itemLeftParen, context, "opening parenthesis"); err != nil {
		return nil, err
	}
	for {
		var expression Expression
		var err e.Error
		next := t.nextNonSpace()
		if next.typ == itemIdentifier {
			identifier := next.val
			next2 := t.nextNonSpace()
			switch next2.typ {
			case itemComma, itemRightParen:
				block.List = append(block.List, BlockParameter{Identifier: identifier})
				next = next2
			case itemAssign:
				expression, next, err = t.parseExpression(context)
				if err != nil {
					return nil, err
				}
				block.List = append(block.List, BlockParameter{Identifier: identifier, Expression: expression})
			default:
				if !isDeclaring {
					switch next2.typ {
					case itemComma, itemRightParen:
					default:
						t.backup2(next)
						expression, next, err = t.parseExpression(context)
						if err != nil {
							return nil, err
						}
						block.List = append(block.List, BlockParameter{Expression: expression})
					}
				} else {
					return nil, t.unexpected(next2, context, "comma, assignment, or closing parenthesis")
				}
			}
		} else if !isDeclaring {
			switch next.typ {
			case itemComma, itemRightParen:
			default:
				t.backup()
				expression, next, err = t.parseExpression(context)
				block.List = append(block.List, BlockParameter{Expression: expression})
			}
		}

		if next.typ != itemComma {
			t.backup()
			break
		}
	}
	if err := t.expect(itemRightParen, context, "closing parenthesis"); err != nil {
		return nil, err
	}
	return block, nil
}

func (t *Template) parseBlock() (Node, e.Error) {
	const context = "block clause"
	var pipe Expression

	name, err := t.expectI(itemIdentifier, context, "name")
	if err != nil {
		return nil, err
	}
	bplist, err := t.blockParametersList(true, context)
	if err != nil {
		return nil, err
	}

	if t.peekNonSpace().typ != itemRightDelim {
		pipe, err = t.expression(context, "context")
		if err != nil {
			return nil, err
		}
	}

	if err = t.expectRightDelim(context); err != nil {
		return nil, err
	}

	list, end, err := t.itemList(nodeContent, nodeEnd)
	if err != nil {
		return nil, err
	}
	var contentList *ListNode

	if end.Type() == nodeContent {
		contentList, end, err = t.itemList(nodeEnd)
		if err != nil {
			return nil, err
		}
	}

	block := t.newBlock(name.pos, t.lex.lineNumber(), name.val, bplist, pipe, list, contentList)
	t.passedBlocks[block.Name] = block
	return block, nil
}

func (t *Template) parseYield() (Node, e.Error) {
	const context = "yield clause"

	var (
		pipe    Expression
		name    item
		bplist  *BlockParameterList
		content *ListNode
		err     e.Error
	)

	// parse block name
	name = t.nextNonSpace()
	if name.typ == itemContent {
		// content yield {{yield content}}
		if t.peekNonSpace().typ != itemRightDelim {
			pipe, err = t.expression(context, "content context")
			if err != nil {
				return nil, err
			}
		}
		if err = t.expectRightDelim(context); err != nil {
			return nil, err
		}
		return t.newYield(name.pos, t.lex.lineNumber(), "", nil, pipe, nil, true), nil
	} else if name.typ != itemIdentifier {
		return nil, t.unexpected(name, context, "block name")
	}

	// parse block parameters
	bplist, err = t.blockParametersList(false, context)
	if err != nil {
		return nil, err
	}

	// parse optional context & content
	typ := t.peekNonSpace().typ
	if typ == itemRightDelim {
		if err = t.expectRightDelim(context); err != nil {
			return nil, err
		}
	} else {
		if typ != itemContent {
			// parse context expression
			pipe, err = t.expression("yield", "context")
			if err != nil {
				return nil, err
			}
			typ = t.peekNonSpace().typ
		}
		if typ == itemRightDelim {
			if err = t.expectRightDelim(context); err != nil {
				return nil, err
			}
		} else if typ == itemContent {
			// parse content from following nodes (until {{end}})
			t.nextNonSpace()
			if err := t.expectRightDelim(context); err != nil {
				return nil, err
			}
			content, _, err = t.itemList(nodeEnd)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, t.unexpected(t.nextNonSpace(), context, "content keyword or closing delimiter")
		}
	}

	return t.newYield(name.pos, t.lex.lineNumber(), name.val, bplist, pipe, content, false), nil
}

func (t *Template) parseInclude() (Node, e.Error) {
	var context Expression
	var err e.Error
	name, err := t.expression("include", "template name")
	if err != nil {
		return nil, err
	}
	if t.peekNonSpace().typ != itemRightDelim {
		context, err = t.expression("include", "context")
		if err != nil {
			return nil, err
		}
	}
	if err = t.expectRightDelim("include invocation"); err != nil {
		return nil, err
	}
	return t.newInclude(name.Position(), t.lex.lineNumber(), name, context), nil
}

func (t *Template) parseReturn() (Node, e.Error) {
	value, err := t.expression("return", "value")
	if err != nil {
		return nil, err
	}
	if err = t.expectRightDelim("return"); err != nil {
		return nil, err
	}
	return t.newReturn(value.Position(), t.lex.lineNumber(), value), nil
}

// itemList:
//
//	textOrAction*
//
// Terminates at any of the given nodes, returned separately.
func (t *Template) itemList(terminatedBy ...NodeType) (list *ListNode, next Node, err e.Error) {
	list = t.newList(t.peekNonSpace().pos)
	for t.peekNonSpace().typ != itemEOF {
		n, err := t.textOrAction()
		if err != nil {
			return nil, nil, err
		}
		for _, terminatorType := range terminatedBy {
			if n.Type() == terminatorType {
				return list, n, nil
			}
		}
		list.append(n)
	}

	return list, next, t.error(e.UnexpectedReason, "unexpected EOF")
}

// textOrAction:
//
//	text | action
func (t *Template) textOrAction() (Node, e.Error) {
	switch token := t.nextNonSpace(); token.typ {
	case itemText:
		return t.newText(token.pos, token.val), nil
	case itemLeftDelim:
		return t.action()
	default:
		return nil, t.unexpected(token, "input", "text or action")
	}
}

func (t *Template) action() (n Node, err e.Error) {
	switch token := t.nextNonSpace(); token.typ {
	case itemInclude:
		return t.parseInclude()
	case itemBlock:
		return t.parseBlock()
	case itemEnd:
		return t.endControl()
	case itemYield:
		return t.parseYield()
	case itemContent:
		return t.contentControl()
	case itemIf:
		return t.ifControl()
	case itemElse:
		return t.elseControl()
	case itemRange:
		return t.rangeControl()
	case itemTry:
		return t.parseTry()
	case itemCatch:
		return t.parseCatch()
	case itemReturn:
		return t.parseReturn()
	}

	t.backup()
	action := t.newAction(t.peek().pos, t.lex.lineNumber())

	expr, err := t.assignmentOrExpression("command")
	if err != nil {
		return nil, err
	}
	if expr.Type() == NodeSet {
		action.Set = expr.(*SetNode)
		expr = nil
		item, err := t.expectOneOf(itemSemicolon, itemRightDelim, "command", "semicolon or right delimiter")
		if err != nil {
			return nil, err
		}
		if item.typ == itemSemicolon {
			expr, err = t.expression("command", "pipeline base expression")
			if err != nil {
				return nil, err
			}
		}
	}
	if expr != nil {
		action.Pipe, err = t.pipeline("command", expr)
		if err != nil {
			return nil, err
		}
	}
	return action, nil
}

func (t *Template) logicalExpression(context string) (Expression, item, e.Error) {
	left, endtoken, err := t.comparativeExpression(context)
	if err != nil {
		return nil, item{}, err
	}
	for endtoken.typ == itemAnd || endtoken.typ == itemOr {
		right, rightendtoken, err := t.comparativeExpression(context)
		if err != nil {
			return nil, item{}, err
		}
		left, endtoken = t.newLogicalExpr(left.Position(), t.lex.lineNumber(), left, right, endtoken), rightendtoken
	}
	return left, endtoken, nil
}

func (t *Template) parseExpression(context string) (Expression, item, e.Error) {
	expression, endtoken, err := t.logicalExpression(context)
	if err != nil {
		return nil, item{}, err
	}
	if endtoken.typ == itemTernary {
		var left, right Expression
		left, endtoken, err := t.parseExpression(context)
		if err != nil {
			return nil, item{}, err
		}
		if endtoken.typ != itemColon {
			if err = t.unexpected(endtoken, "ternary expression", "colon in ternary expression"); err != nil {
				return nil, item{}, err
			}
		}
		right, endtoken, err = t.parseExpression(context)
		if err != nil {
			return nil, item{}, err
		}
		expression = t.newTernaryExpr(expression.Position(), t.lex.lineNumber(), expression, left, right)
	}
	return expression, endtoken, nil
}

func (t *Template) comparativeExpression(context string) (Expression, item, e.Error) {
	left, endtoken, err := t.numericComparativeExpression(context)
	if err != nil {
		return nil, item{}, err
	}
	for endtoken.typ == itemEquals || endtoken.typ == itemNotEquals {
		right, rightendtoken, err := t.numericComparativeExpression(context)
		if err != nil {
			return nil, item{}, err
		}
		left, endtoken = t.newComparativeExpr(left.Position(), t.lex.lineNumber(), left, right, endtoken), rightendtoken
	}
	return left, endtoken, nil
}

func (t *Template) numericComparativeExpression(context string) (Expression, item, e.Error) {
	left, endtoken, err := t.additiveExpression(context)
	if err != nil {
		return nil, item{}, err
	}
	for endtoken.typ >= itemGreat && endtoken.typ <= itemLessEquals {
		right, rightendtoken, err := t.additiveExpression(context)
		if err != nil {
			return nil, item{}, err
		}
		left, endtoken = t.newNumericComparativeExpr(left.Position(), t.lex.lineNumber(), left, right, endtoken), rightendtoken
	}
	return left, endtoken, nil
}

func (t *Template) additiveExpression(context string) (Expression, item, e.Error) {
	left, endtoken, err := t.multiplicativeExpression(context)
	if err != nil {
		return nil, item{}, err
	}
	for endtoken.typ == itemAdd || endtoken.typ == itemMinus {
		right, rightendtoken, err := t.multiplicativeExpression(context)
		if err != nil {
			return nil, item{}, err
		}
		left, endtoken = t.newAdditiveExpr(left.Position(), t.lex.lineNumber(), left, right, endtoken), rightendtoken
	}
	return left, endtoken, nil
}

func (t *Template) multiplicativeExpression(context string) (left Expression, endtoken item, err e.Error) {
	left, endtoken, err = t.unaryExpression(context)
	if err != nil {
		return nil, item{}, err
	}
	for endtoken.typ >= itemMul && endtoken.typ <= itemMod {
		right, rightendtoken, err := t.unaryExpression(context)
		if err != nil {
			return nil, item{}, err
		}
		left, endtoken = t.newMultiplicativeExpr(left.Position(), t.lex.lineNumber(), left, right, endtoken), rightendtoken
	}

	return left, endtoken, nil
}

func (t *Template) unaryExpression(context string) (Expression, item, e.Error) {
	next := t.nextNonSpace()
	switch next.typ {
	case itemNot:
		expr, endToken, err := t.comparativeExpression(context)
		if err != nil {
			return nil, item{}, err
		}
		return t.newNotExpr(expr.Position(), t.lex.lineNumber(), expr), endToken, nil
	case itemMinus, itemAdd:
		operand, err := t.operand("additive expression")
		if err != nil {
			return nil, item{}, err
		}
		return t.newAdditiveExpr(next.pos, t.lex.lineNumber(), nil, operand, next), t.nextNonSpace(), nil
	default:
		t.backup()
	}
	operand, err := t.operand(context)
	if err != nil {
		return nil, item{}, err
	}
	return operand, t.nextNonSpace(), nil
}

func (t *Template) assignmentOrExpression(context string) (operand Expression, err e.Error) {
	t.peekNonSpace()
	line := t.lex.lineNumber()
	var right, left []Expression

	var isSet bool
	var isLet bool
	var returned item
	operand, returned, err = t.parseExpression(context)
	if err != nil {
		return nil, err
	}
	pos := operand.Position()
	if returned.typ == itemComma || returned.typ == itemAssign {
		isSet = true
	} else {
		if operand == nil {
			if err = t.unexpected(returned, context, "operand"); err != nil {
				return nil, err
			}
		}
		t.backup()
		return operand, nil
	}

	if isSet {
	leftloop:
		for {
			switch operand.Type() {
			case NodeField, NodeChain, NodeIdentifier, NodeUnderscore:
				left = append(left, operand)
			default:
				return nil, t.error(e.UnexpectedNodeReason, "unexpected node in assign")
			}

			switch returned.typ {
			case itemComma:
				operand, returned, err = t.parseExpression(context)
				if err != nil {
					return nil, err
				}
			case itemAssign:
				isLet = returned.val == ":="
				break leftloop
			default:
				if err = t.unexpected(returned, "assignment", "comma or assignment"); err != nil {
					return nil, err
				}
			}
		}

		if isLet {
			for _, operand := range left {
				if operand.Type() != NodeIdentifier && operand.Type() != NodeUnderscore {
					if err = t.error(e.UnexpectedNodeTypeReason, fmt.Sprintf("unexpected node type %s in variable declaration", operand)); err != nil {
						return nil, err
					}
				}
			}
		}

		for {
			operand, returned, err = t.parseExpression("assignment")
			if err != nil {
				return nil, err
			}
			right = append(right, operand)
			if returned.typ != itemComma {
				t.backup()
				break
			}
		}

		var isIndexExprGetLookup bool

		if context == "range" {
			if len(left) > 2 || len(right) > 1 {
				if err = t.error("unexpected.number_of_operands", "unexpected number of operands in assign on range"); err != nil {
					return nil, err
				}
			}
		} else {
			if len(left) != len(right) {
				if len(left) == 2 && len(right) == 1 && right[0].Type() == NodeIndexExpr {
					isIndexExprGetLookup = true
				} else {
					if err = t.error("unexpected.number_of_operands", "unexpected number of operands in assign on range"); err != nil {
						return nil, err
					}
				}
			}
		}
		operand = t.newSet(pos, line, isLet, isIndexExprGetLookup, left, right)
		return

	}
	return operand, nil
}

func (t *Template) expression(context, as string) (Expression, e.Error) {
	expr, tk, err := t.parseExpression(context)
	if err != nil {
		return nil, err
	}
	if expr == nil {
		if err := t.unexpected(tk, context, as); err != nil {
			return nil, err
		}
	}
	t.backup()
	return expr, nil
}

func (t *Template) pipeline(context string, baseExprMutate Expression) (pipe *PipeNode, err e.Error) {
	pos := t.peekNonSpace().pos
	pipe = t.newPipeline(pos, t.lex.lineNumber())

	if baseExprMutate == nil {
		if err = pipe.error("invalid.expression", "parsing pipeline: first expression cannot be nil"); err != nil {
			return nil, err
		}
	}
	command, err := t.command(baseExprMutate)
	if err != nil {
		return nil, err
	}
	pipe.append(command)

	for {
		token, err := t.expectOneOf(itemPipe, itemRightDelim, "pipeline", "pipe or right delimiter")
		if err != nil {
			return nil, err
		}
		if token.typ == itemRightDelim {
			break
		}
		token = t.nextNonSpace()
		switch token.typ {
		case itemField, itemIdentifier:
			t.backup()
			command, err = t.command(nil)
			if err != nil {
				return nil, err
			}
			pipe.append(command)
		default:
			if err = t.unexpected(token, "pipeline", "field or identifier"); err != nil {
				return nil, err
			}
		}
	}

	return pipe, nil
}

func (t *Template) command(baseExpr Expression) (*CommandNode, e.Error) {
	cmd := t.newCommand(t.peekNonSpace().pos)

	var err e.Error
	if baseExpr == nil {
		baseExpr, err = t.expression("command", "name")
		if err != nil {
			return nil, err
		}
	}

	if baseExpr.Type() == NodeCallExpr {
		call := baseExpr.(*CallExprNode)
		cmd.CallExprNode = *call
		return cmd, nil
	}

	cmd.BaseExpr = baseExpr

	next := t.nextNonSpace()
	switch next.typ {
	case itemColon:
		callArgs, err := t.parseArguments()
		if err != nil {
			return nil, err
		}
		cmd.CallArgs = callArgs
	default:
		t.backup()
	}

	if cmd.BaseExpr == nil {
		if err = t.error("empty.command", "empty command"); err != nil {
			return nil, err
		}
	}

	return cmd, nil
}

// operand:
//
//	term .Field*
//
// An operand is a space-separated component of a command,
// a term possibly followed by field accesses.
// A nil return means the next item is not an operand.
func (t *Template) operand(context string) (Expression, e.Error) {
	node, err := t.term()
	if err != nil {
		return nil, err
	}
	if node == nil {
		if err = t.unexpected(t.next(), context, "term"); err != nil {
			return nil, err
		}
	}
	lefBracketHandler := func(node Node, nullable bool) (Node, e.Error) {
		base := node
		var index Expression
		var next item

		// found colon is slice expression
		if t.peekNonSpace().typ != itemColon {
			index, next, err = t.parseExpression("index|slice expression")
			if err != nil {
				return nil, err
			}
		} else {
			next = t.nextNonSpace()
		}

		switch next.typ {
		case itemColon:
			var endIndex Expression
			if t.peekNonSpace().typ != itemRightBrackets {
				endIndex, err = t.expression("slice expression", "end indexß")
				if err != nil {
					return nil, err
				}
			}
			node = t.newSliceExpr(node.Position(), node.line(), base, index, endIndex)
		case itemRightBrackets:
			node = t.newIndexExpr(node.Position(), node.line(), base, index, nullable)
			fallthrough
		default:
			t.backup()
		}

		if err = t.expect(itemRightBrackets, "index expression", "closing bracket"); err != nil {
			return nil, err
		}

		return node, nil
	}

	for {
		peek := t.peek()
		if peek.typ == itemField || peek.typ == itemLaxField {
			chain := t.newChain(t.peek().pos, node)
			for t.peekNonSpace().typ == itemField || t.peekNonSpace().typ == itemLaxField {
				chain.Add(t.next().val)
			}
			// Compatibility with original API: If the term is of type NodeField
			// or NodeVariable, just put more fields on the original.
			// Otherwise, keep the Chain node.
			// Obvious parsing errors involving literal values are detected here.
			// More complex error cases will have to be handled at execution time.
			switch node.Type() {
			case NodeField:
				node = t.newField(chain.Position(), chain.String(), peek.typ == itemLaxField)
			case NodeBool, NodeString, NodeNumber, NodeNil:
				if err = t.error(e.UnexpectedReason, fmt.Sprintf("unexpected . after term %q", node.String())); err != nil {
					return nil, err
				}
			default:
				node = chain
			}
		}
		nodeTYPE := node.Type()
		if nodeTYPE == NodeIdentifier ||
			nodeTYPE == NodeCallExpr ||
			nodeTYPE == NodeField ||
			nodeTYPE == NodeChain ||
			nodeTYPE == NodeIndexExpr {
			switch t.nextNonSpace().typ {
			case itemLeftParen:
				callExpr := t.newCallExpr(node.Position(), t.lex.lineNumber(), node)
				callArgs, err := t.parseArguments()
				if err != nil {
					return nil, err
				}
				callExpr.CallArgs = callArgs
				if err = t.expect(itemRightParen, "call expression", "closing parenthesis"); err != nil {
					return nil, err
				}
				node = callExpr
				continue
			case itemLeftBrackets:
				node, err = lefBracketHandler(node, false)
				if err != nil {
					return nil, err
				}
				continue
			case itemLeftLaxBrackets:
				node, err = lefBracketHandler(node, true)
				if err != nil {
					return nil, err
				}
				continue
			case itemLaxField:
				node = t.newField(node.Position(), node.String(), true)
				continue
			default:
				t.backup()
			}
		}
		return node, nil
	}
}

func (t *Template) parseArguments() (args CallArgs, err e.Error) {
	context := "call expression argument list"
	args.Exprs = []Expression{}
loop:
	for {
		peek := t.peekNonSpace()
		if peek.typ == itemRightParen {
			break
		}
		var (
			expr     Expression
			endtoken item
		)
		expr, endtoken, err = t.parseExpression(context)
		if err != nil {
			return CallArgs{}, err
		}
		if expr.Type() == NodeUnderscore {
			// slot for piped argument
			if args.HasPipeSlot {
				if err = t.error("conflict", "found two pipe slot markers ('_') for the same function call"); err != nil {
					return CallArgs{}, err
				}
			}
			args.HasPipeSlot = true
		}
		args.Exprs = append(args.Exprs, expr)
		switch endtoken.typ {
		case itemComma:
			// continue with closing parens (allowed because of multiline syntax) or next arg
		default:
			t.backup()
			break loop
		}
	}
	return
}

func (t *Template) parseControl(allowElseIf bool, context string) (pos Pos, line int, set *SetNode, expression Expression, list, elseList *ListNode, err e.Error) {
	line = t.lex.lineNumber()

	expression, err = t.assignmentOrExpression(context)
	if err != nil {
		return
	}
	pos = expression.Position()
	if expression.Type() == NodeSet {
		set = expression.(*SetNode)
		if context != "range" {
			err = t.expect(itemSemicolon, context, "semicolon between assignment and expression")
			if err != nil {
				return
			}
			expression, err = t.expression(context, "expression after assignment")
			if err != nil {
				return
			}
		} else {
			expression = nil
		}
	}

	if err = t.expectRightDelim(context); err != nil {
		return
	}
	var next Node
	var ifControl Node
	list, next, err = t.itemList(nodeElse, nodeEnd)
	if err != nil {
		return
	}
	if next.Type() == nodeElse {
		if allowElseIf && t.peek().typ == itemIf {
			// Special case for "else if". If the "else" is followed immediately by an "if",
			// the elseControl will have left the "if" token pending. Treat
			//	{{if a}}_{{else if b}}_{{end}}
			// as
			//	{{if a}}_{{else}}{{if b}}_{{end}}{{end}}.
			// To do this, parse the if as usual and stop at it {{end}}; the subsequent{{end}}
			// is assumed. This technique works even for long if-else-if chains.
			t.next() // Consume the "if" token.
			elseList = t.newList(next.Position())
			ifControl, err = t.ifControl()
			if err != nil {
				return
			}
			elseList.append(ifControl)
			// Do not consume the next item - only one {{end}} required.
		} else {
			elseList, next, err = t.itemList(nodeEnd)
			if err != nil {
				return
			}
		}
	}
	return pos, line, set, expression, list, elseList, nil
}

// If:
//
//	{{if expression}} itemList {{end}}
//	{{if expression}} itemList {{else}} itemList {{end}}
//
// If keyword is past.
func (t *Template) ifControl() (Node, e.Error) {
	pos, line, set, expression, list, elseList, err := t.parseControl(true, "if")
	if err != nil {
		return nil, err
	}
	return t.newIf(pos, line, set, expression, list, elseList), nil
}

// Range:
//
//	{{range expression}} itemList {{end}}
//	{{range expression}} itemList {{else}} itemList {{end}}
//
// Range keyword is past.
func (t *Template) rangeControl() (Node, e.Error) {
	pos, line, set, expression, list, elseList, err := t.parseControl(false, "range")
	if err != nil {
		return nil, err
	}
	return t.newRange(pos, line, set, expression, list, elseList), nil
}

// End:
//
//	{{end}}
//
// End keyword is past.
func (t *Template) endControl() (Node, e.Error) {
	item, err := t.expectRightDelimI("end")
	if err != nil {
		return nil, err
	}
	return t.newEnd(item.pos), nil
}

// Content:
//
//	{{content}}
//
// Content keyword is past.
func (t *Template) contentControl() (Node, e.Error) {
	item, err := t.expectRightDelimI("content")
	if err != nil {
		return nil, err
	}
	return t.newContent(item.pos), nil
}

// Else:
//
//	{{else}}
//
// Else keyword is past.
func (t *Template) elseControl() (Node, e.Error) {
	// Special case for "else if".
	peek := t.peekNonSpace()
	if peek.typ == itemIf {
		// We see "{{else if ... " but in effect rewrite it to {{else}}{{if ... ".
		return t.newElse(peek.pos, t.lex.lineNumber()), nil
	}
	item, err := t.expectRightDelimI("else")
	if err != nil {
		return nil, err
	}
	return t.newElse(item.pos, t.lex.lineNumber()), nil
}

// Try-catch:
//
//		{{try}}
//	   itemList
//	 {{catch <ident>}}
//	   itemList
//	 {{end}}
//
// try keyword is past.
func (t *Template) parseTry() (*TryNode, e.Error) {
	var recov *catchNode
	line := t.lex.lineNumber()
	item, err := t.expectRightDelimI("try")
	if err != nil {
		return nil, err
	}
	pos := item.pos
	list, next, err := t.itemList(nodeCatch, nodeEnd)
	if err != nil {
		return nil, err
	}
	if next.Type() == nodeCatch {
		recov = next.(*catchNode)
	}

	return t.newTry(pos, line, list, recov), nil
}

// catch:
//
//	{{catch <ident>}}
//	  itemList
//	{{end}}
//
// catch keyword is past.
func (t *Template) parseCatch() (*catchNode, e.Error) {
	line := t.lex.lineNumber()
	var errVar *IdentifierNode
	peek := t.peekNonSpace()
	if peek.typ != itemRightDelim {
		_errVar, err := t.term()
		if err != nil {
			return nil, err
		}
		if typ := _errVar.Type(); typ != NodeIdentifier {
			return nil, t.error(e.UnexpectedNodeTypeReason, fmt.Sprintf("unexpected node type '%v' in catch", typ))
		}
		errVar = _errVar.(*IdentifierNode)
	}
	if err := t.expectRightDelim("catch"); err != nil {
		return nil, err
	}
	list, _, err := t.itemList(nodeEnd)
	if err != nil {
		return nil, err
	}
	return t.newCatch(peek.pos, line, errVar, list), nil
}

// term:
//
//	literal (number, string, nil, boolean)
//	function (identifier)
//	.
//	.Field
//
// ?.Field
//
//	variable
//	'(' expression ')'
//
// A term is a simple "expression".
// A nil return means the next item is not a term.
func (t *Template) term() (Node, e.Error) {
	switch token := t.nextNonSpace(); token.typ {
	case itemError:
		return nil, t.error("item.error", fmt.Sprintf("%s", token.val))
	case itemIdentifier:
		return t.newIdentifier(token.val, token.pos, t.lex.lineNumber()), nil
	case itemUnderscore:
		return t.newUnderscore(token.pos, t.lex.lineNumber()), nil
	case itemNil:
		return t.newNil(token.pos), nil
	case itemField:
		return t.newField(token.pos, token.val, false), nil
	case itemLaxField:
		return t.newField(token.pos, token.val, true), nil
	case itemBool:
		return t.newBool(token.pos, token.val == "true"), nil
	case itemCharConstant, itemComplex, itemNumber:
		number, err := t.newNumber(token.pos, token.val, token.typ)
		if err != nil {
			return nil, t.error("", err.Error())
		}
		return number, nil
	case itemLeftParen:
		pipe, err := t.expression("parenthesized expression", "expression")
		if err != nil {
			return nil, err
		}
		if token := t.next(); token.typ != itemRightParen {
			return nil, t.unexpected(token, "parenthesized expression", "closing parenthesis")
		}
		return pipe, nil
	case itemString, itemRawString:
		s, err := unquote(token.val)
		if err != nil {
			return nil, t.error("", err.Error())
		}
		return t.newString(token.pos, token.val, s), nil
	}
	t.backup()
	return nil, nil
}
