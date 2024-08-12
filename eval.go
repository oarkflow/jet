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
	"fmt"
	"io"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/oarkflow/jet/fastprinter"
	"github.com/oarkflow/jet/utils/e"
)

var (
	funcType       = reflect.TypeOf(Func(nil))
	stringerType   = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
	rangerType     = reflect.TypeOf((*Ranger)(nil)).Elem()
	rendererType   = reflect.TypeOf((*Renderer)(nil)).Elem()
	safeWriterType = reflect.TypeOf(SafeWriter(nil))
	pool_State     = sync.Pool{
		New: func() interface{} {
			return &Runtime{scope: &scope{}, escapeeWriter: new(escapeeWriter)}
		},
	}
)

// Renderer is used to detect if a value has its own rendering logic. If the value an action evaluates to implements this
// interface, it will not be printed using github.com/CloudyKit/fastprinter, instead, its Render() method will be called
// and is responsible for writing the value to the render output.
type Renderer interface {
	Render(*Runtime)
}

// RendererFunc func implementing interface Renderer
type RendererFunc func(*Runtime)

func (renderer RendererFunc) Render(r *Runtime) {
	renderer(r)
}

type escapeeWriter struct {
	Writer  io.Writer
	escapee SafeWriter
	set     *Set
}

func (w *escapeeWriter) Write(b []byte) (int, error) {
	if w.set == nil || w.set.escapee == nil {
		w.Writer.Write(b)
	} else {
		w.set.escapee(w.Writer, b)
	}
	return 0, nil
}

// Runtime this type holds the state of the execution of an template
type Runtime struct {
	*escapeeWriter
	*scope
	content func(*Runtime, Expression) e.Error

	context reflect.Value
}

// Context returns the current context value
func (rt *Runtime) Context() reflect.Value {
	return rt.context
}

func (rt *Runtime) newScope() {
	rt.scope = &scope{parent: rt.scope, variables: make(VarMap), blocks: rt.blocks}
}

func (rt *Runtime) releaseScope() {
	rt.scope = rt.scope.parent
}

type scope struct {
	parent    *scope
	variables VarMap
	blocks    map[string]*BlockNode
}

func (s *scope) sortedBlocks() []string {
	r := make([]string, 0, len(s.blocks))
	for k := range s.blocks {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}

// YieldBlock yields a block in the current context, will panic if the context is not available
func (rt *Runtime) YieldBlock(name string, context interface{}) e.Error {
	block, has := rt.getBlock(name)

	if has == false {
		return e.New().
			WithReason("not_found.block").
			WithMessage(fmt.Sprintf("block %q was not found!!", name))
	}

	if context != nil {
		current := rt.context
		rt.context = reflect.ValueOf(context)
		if _, err := rt.executeList(block.List); err != nil {
			return err
		}
		rt.context = current
	}

	_, err := rt.executeList(block.List)
	return err
}

func (s *scope) getBlock(name string) (block *BlockNode, has bool) {
	block, has = s.blocks[name]
	for !has && s.parent != nil {
		s = s.parent
		block, has = s.blocks[name]
	}
	return
}

func (rt *Runtime) setValue(name string, val reflect.Value) e.Error {
	// try changing existing variable in current or parent scope
	sc := rt.scope
	for sc != nil {
		if _, ok := sc.variables[name]; ok {
			sc.variables[name] = val
			return nil
		}
		sc = sc.parent
	}

	return e.New().
		WithReason("invalid.variable").
		WithMessage(fmt.Sprintf("could not assign %q = %v because variable %q is uninitialised", name, val, name))
}

// LetGlobal sets or initialises a variable in the top-most template scope.
func (rt *Runtime) LetGlobal(name string, val interface{}) {
	sc := rt.scope

	// walk up to top-most valid scope
	for sc.parent != nil && sc.parent.variables != nil {
		sc = sc.parent
	}

	sc.variables[name] = reflect.ValueOf(val)
}

// Set sets an existing variable in the template scope it lives in.
func (rt *Runtime) Set(name string, val interface{}) error {
	return rt.setValue(name, reflect.ValueOf(val))
}

// Let initialises a variable in the current template scope (possibly shadowing an existing variable of the same name in a parent scope).
func (rt *Runtime) Let(name string, val interface{}) {
	rt.scope.variables[name] = reflect.ValueOf(val)
}

// SetOrLet calls Set() (if a variable with the given name is visible from the current scope) or Let() (if there is no variable with the given name in the current or any parent scope).
func (rt *Runtime) SetOrLet(name string, val interface{}) {
	_, err := rt.resolve(name)
	if err != nil {
		rt.Let(name, val)
	} else {
		rt.Set(name, val)
	}
}

// Resolve resolves a value from the execution context.
func (rt *Runtime) resolve(name string) (reflect.Value, e.Error) {
	if name == "." {
		return rt.context, nil
	}

	// try current, then parent variable scopes
	sc := rt.scope
	for sc != nil {
		v, ok := sc.variables[name]
		if ok {
			return indirectEface(v), nil
		}
		sc = sc.parent
	}

	// try globals
	rt.set.gmx.RLock()
	v, ok := rt.set.globals[name]
	rt.set.gmx.RUnlock()
	if ok {
		return indirectEface(v), nil
	}

	// try default variables
	v, ok = defaultVariables[name]
	if ok {
		return indirectEface(v), nil
	}

	return reflect.Value{}, e.New().
		WithReason("not_available.identifier").
		WithMessage(fmt.Sprintf("identifier %q not available in current (%+v) or parent scope, global, or default variables", name, rt.scope.variables))
}

// Resolve calls resolve() and ignores any errors, meaning it may return a zero reflect.Value.
func (rt *Runtime) Resolve(name string) reflect.Value {
	v, _ := rt.resolve(name)
	return v
}

// Resolve calls resolve() and panics if there is an error.
func (rt *Runtime) MustResolve(name string) reflect.Value {
	v, err := rt.resolve(name)
	if err != nil {
		panic(err)
	}
	return v
}

func (rt *Runtime) recover(err *error) {
	// reset state scope and context just to be safe (they might not be cleared properly if there was a panic while using the state)
	rt.scope = &scope{}
	rt.context = reflect.Value{}
	pool_State.Put(rt)
	if recovered := recover(); recovered != nil {
		var ok bool
		if _, ok = recovered.(runtime.Error); ok {
			panic(recovered)
		}
		*err, ok = recovered.(error)
		if !ok {
			panic(recovered)
		}
	}
}

func (rt *Runtime) executeSet(left Expression, right reflect.Value) e.Error {
	typ := left.Type()
	if typ == NodeIdentifier {
		return rt.setValue(left.(*IdentifierNode).Ident, right)
	}
	var value reflect.Value
	var err e.Error
	var fields Idents
	if typ == NodeChain {
		chain := left.(*ChainNode)
		value, err = rt.evalPrimaryExpressionGroup(chain.Node)
		if err != nil {
			return err
		}
		fields = chain.Field
	} else {
		fields = left.(*FieldNode).Idents
		value = rt.context
	}
	lef := len(fields) - 1
	for i := 0; i < lef; i++ {
		value, err = resolveIndex(value, reflect.Value{}, fields[i].name, fields[i].lax)
		if err != nil {
			return left.error(err.Reason(), err.Message())
		}
	}

	for {
		switch value.Kind() {
		case reflect.Ptr:
			value = value.Elem()
			continue
		case reflect.Struct:
			value = value.FieldByName(fields[lef].name)
			if !value.IsValid() {
				return left.error(
					"not_available.identifier",
					fmt.Sprintf("identifier %v is not available in the current scope", fields[lef]),
				)
			}
			value.Set(right)
		case reflect.Map:
			value.SetMapIndex(reflect.ValueOf(&fields[lef]).Elem(), right)
		}
		break
	}

	return nil
}

func (rt *Runtime) executeSetList(set *SetNode) e.Error {
	if set.IndexExprGetLookup {
		value, err := rt.evalPrimaryExpressionGroup(set.Right[0])
		if err != nil {
			return err
		}
		if set.Left[0].Type() != NodeUnderscore {
			if err := rt.executeSet(set.Left[0], value); err != nil {
				return err
			}
		}
		if set.Left[1].Type() != NodeUnderscore {
			if value.IsValid() {
				if err := rt.executeSet(set.Left[1], valueBoolTRUE); err != nil {
					return err
				}
			} else {
				if err := rt.executeSet(set.Left[1], valueBoolFALSE); err != nil {
					return err
				}
			}
		}
	} else {
		for i := 0; i < len(set.Left); i++ {
			value, err := rt.evalPrimaryExpressionGroup(set.Right[i])
			if err != nil {
				return err
			}
			if set.Left[i].Type() != NodeUnderscore {
				if err := rt.executeSet(set.Left[i], value); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (rt *Runtime) executeLetList(set *SetNode) e.Error {
	if set.IndexExprGetLookup {
		value, err := rt.evalPrimaryExpressionGroup(set.Right[0])
		if err != nil {
			return err
		}
		if set.Left[0].Type() != NodeUnderscore {
			rt.variables[set.Left[0].(*IdentifierNode).Ident] = value
		}
		if set.Left[1].Type() != NodeUnderscore {
			if value.IsValid() {
				rt.variables[set.Left[1].(*IdentifierNode).Ident] = valueBoolTRUE
			} else {
				rt.variables[set.Left[1].(*IdentifierNode).Ident] = valueBoolFALSE
			}
		}
	} else {
		for i := 0; i < len(set.Left); i++ {
			value, err := rt.evalPrimaryExpressionGroup(set.Right[i])
			if err != nil {
				return err
			}
			if set.Left[i].Type() != NodeUnderscore {
				rt.variables[set.Left[i].(*IdentifierNode).Ident] = value
			}
		}
	}

	return nil
}

func (rt *Runtime) executeYieldBlock(block *BlockNode, blockParam, yieldParam *BlockParameterList, expression Expression, content *ListNode) e.Error {
	needNewScope := len(blockParam.List) > 0 || len(yieldParam.List) > 0
	if needNewScope {
		rt.newScope()
		for i := 0; i < len(yieldParam.List); i++ {
			p := &yieldParam.List[i]

			if p.Expression == nil {
				return block.error(
					"missing.name",
					fmt.Sprintf("missing name for block parameter '%s'", blockParam.List[i].Identifier),
				)
			}

			exp, err := rt.evalPrimaryExpressionGroup(p.Expression)
			if err != nil {
				return err
			}
			rt.variables[p.Identifier] = exp
		}
		for i := 0; i < len(blockParam.List); i++ {
			p := &blockParam.List[i]
			if _, found := rt.variables[p.Identifier]; !found {
				if p.Expression == nil {
					rt.variables[p.Identifier] = valueBoolFALSE
				} else {
					exp, err := rt.evalPrimaryExpressionGroup(p.Expression)
					if err != nil {
						return err
					}
					rt.variables[p.Identifier] = exp
				}
			}
		}
	}

	mycontent := rt.content
	if content != nil {
		myscope := rt.scope
		rt.content = func(st *Runtime, expression Expression) e.Error {
			outscope := st.scope
			outcontent := st.content

			st.scope = myscope
			st.content = mycontent

			if expression != nil {
				context := st.context
				exp, err := st.evalPrimaryExpressionGroup(expression)
				if err != nil {
					return err
				}
				st.context = exp
				_, err = st.executeList(content)
				if err != nil {
					return err
				}
				st.context = context
			} else {
				_, _ = st.executeList(content)
			}

			st.scope = outscope
			st.content = outcontent

			return nil
		}
	}

	if expression != nil {
		context := rt.context
		exp, err := rt.evalPrimaryExpressionGroup(expression)
		if err != nil {
			return err
		}
		rt.context = exp
		_, err = rt.executeList(block.List)
		if err != nil {
			return err
		}
		rt.context = context
	} else {
		_, err := rt.executeList(block.List)
		if err != nil {
			return err
		}
	}

	rt.content = mycontent
	if needNewScope {
		rt.releaseScope()
	}

	return nil
}

func (rt *Runtime) executeList(list *ListNode) (returnValue reflect.Value, err e.Error) {
	inNewScope := false // to use just one scope for multiple actions with variable declarations

	for i := 0; i < len(list.Nodes); i++ {
		node := list.Nodes[i]

		switch node.Type() {
		case NodeText:
			node := node.(*TextNode)
			if _, err := rt.Writer.Write(node.Text); err != nil {
				return reflect.Value{}, node.error("", err.Error())
			}
		case NodeAction:
			node := node.(*ActionNode)
			if node.Set != nil {
				if node.Set.Let {
					if !inNewScope {
						rt.newScope()
						inNewScope = true
						defer rt.releaseScope()
					}
					err = rt.executeLetList(node.Set)
				} else {
					err = rt.executeSetList(node.Set)
				}
			}
			if node.Pipe != nil {
				v, safeWriter, err := rt.evalPipelineExpression(node.Pipe)
				if err != nil {
					return reflect.Value{}, err
				}
				if !safeWriter && v.IsValid() {
					if v.Type().Implements(rendererType) {
						v.Interface().(Renderer).Render(rt)
					} else {
						if _, err := fastprinter.PrintValue(rt.escapeeWriter, v); err != nil {
							return reflect.Value{}, node.error("", err.Error())
						}
					}
				}
			}
		case NodeIf:
			node := node.(*IfNode)
			var isLet bool
			if node.Set != nil {
				if node.Set.Let {
					isLet = true
					rt.newScope()
					err = rt.executeLetList(node.Set)
				} else {
					err = rt.executeSetList(node.Set)
				}
			}
			expression, err := rt.evalPrimaryExpressionGroup(node.Expression)
			if err != nil {
				return reflect.Value{}, err
			}
			if isTrue(expression) {
				returnValue, err = rt.executeList(node.List)
			} else if node.ElseList != nil {
				returnValue, err = rt.executeList(node.ElseList)
			}
			if isLet {
				rt.releaseScope()
			}
		case NodeRange:
			node := node.(*RangeNode)
			var expression reflect.Value

			isSet := node.Set != nil
			isLet := false
			keyVarSlot := 0
			valVarSlot := -1

			context := rt.context

			if isSet {
				if len(node.Set.Left) > 1 {
					valVarSlot = 1
				}
				expression, err = rt.evalPrimaryExpressionGroup(node.Set.Right[0])
				if err != nil {
					return reflect.Value{}, err
				}
				if node.Set.Let {
					isLet = true
					rt.newScope()
				}
			} else {
				expression, err = rt.evalPrimaryExpressionGroup(node.Expression)
				if err != nil {
					return reflect.Value{}, err
				}
			}

			ranger, cleanup, err := getRanger(expression)
			if err != nil {
				return reflect.Value{}, node.error("", err.Error())
			}
			if !ranger.ProvidesIndex() {
				if isSet && len(node.Set.Left) > 1 {
					// two-vars assignment with ranger that doesn't provide an index
					return reflect.Value{}, node.error("", "two-var range over ranger that does not provide an index")
				} else if isSet {
					keyVarSlot, valVarSlot = -1, 0
				}
			}

			indexValue, rangeValue, end := ranger.Range()
			if !end {
				for !end && !returnValue.IsValid() {
					if isSet {
						if isLet {
							if keyVarSlot >= 0 {
								rt.variables[node.Set.Left[keyVarSlot].String()] = indexValue
							}
							if valVarSlot >= 0 {
								rt.variables[node.Set.Left[valVarSlot].String()] = rangeValue
							}
						} else {
							if keyVarSlot >= 0 {
								err = rt.executeSet(node.Set.Left[keyVarSlot], indexValue)
							}
							if valVarSlot >= 0 {
								err = rt.executeSet(node.Set.Left[valVarSlot], rangeValue)
							}
						}
					}
					if valVarSlot < 0 {
						rt.context = rangeValue
					}
					returnValue, err = rt.executeList(node.List)
					indexValue, rangeValue, end = ranger.Range()
				}
			} else if node.ElseList != nil {
				returnValue, err = rt.executeList(node.ElseList)
			}
			cleanup()
			rt.context = context
			if isLet {
				rt.releaseScope()
			}
		case NodeTry:
			node := node.(*TryNode)
			returnValue, err = rt.executeTry(node)
		case NodeYield:
			node := node.(*YieldNode)
			if node.IsContent {
				if rt.content != nil {
					err = rt.content(rt, node.Expression)
				}
			} else {
				block, has := rt.getBlock(node.Name)
				if has == false || block == nil {
					return reflect.Value{}, node.error("unresolved.block", fmt.Sprintf("unresolved block %q!!", node.Name))
				}
				err = rt.executeYieldBlock(block, block.Parameters, node.Parameters, node.Expression, node.Content)
			}
		case NodeBlock:
			node := node.(*BlockNode)
			block, has := rt.getBlock(node.Name)
			if has == false {
				block = node
			}
			err = rt.executeYieldBlock(block, block.Parameters, block.Parameters, block.Expression, block.Content)
		case NodeInclude:
			node := node.(*IncludeNode)
			returnValue, err = rt.executeInclude(node)
		case NodeReturn:
			node := node.(*ReturnNode)
			returnValue, err = rt.evalPrimaryExpressionGroup(node.Value)
		}
	}

	return returnValue, err
}

func (rt *Runtime) executeTry(try *TryNode) (returnValue reflect.Value, err e.Error) {
	writer := rt.Writer
	buf := new(bytes.Buffer)

	defer func() {
		r := recover()

		// copy buffered render output to writer only if no panic occured
		if r == nil {
			io.Copy(writer, buf)
		} else {
			// rt.Writer is already set to its original value since the later defer ran first
			if try.Catch != nil {
				if try.Catch.Err != nil {
					rt.newScope()
					rt.scope.variables[try.Catch.Err.Ident] = reflect.ValueOf(r)
				}
				if try.Catch.List != nil {
					returnValue, err = rt.executeList(try.Catch.List)
				}
				if try.Catch.Err != nil {
					rt.releaseScope()
				}
			}
		}
	}()

	rt.Writer = buf
	defer func() { rt.Writer = writer }()

	return rt.executeList(try.List)
}

func (rt *Runtime) executeInclude(node *IncludeNode) (returnValue reflect.Value, err e.Error) {
	var templatePath string
	name, err := rt.evalPrimaryExpressionGroup(node.Name)
	if err != nil {
		return reflect.Value{}, err
	}
	if !name.IsValid() {
		return reflect.Value{}, node.error(e.InvalidValueReason, "evaluating name of template to include: name is not a valid value")
	}
	if name.Type().Implements(stringerType) {
		templatePath = name.String()
	} else if name.Kind() == reflect.String {
		templatePath = name.String()
	} else {
		return reflect.Value{}, node.error(e.UnexpectedExpressionTypeReason, fmt.Sprintf("evaluating name of template to include: unexpected expression type %q", getTypeString(name)))
	}

	t, getTemplateErr := rt.set.getSiblingTemplate(templatePath, node.TemplatePath, true)
	if err != nil {
		return reflect.Value{}, node.error("", getTemplateErr.Error())
	}

	rt.newScope()
	defer rt.releaseScope()

	rt.blocks = t.processedBlocks

	var context reflect.Value
	if node.Context != nil {
		context = rt.context
		defer func() { rt.context = context }()
		contextExpression, err := rt.evalPrimaryExpressionGroup(node.Context)
		if err != nil {
			return reflect.Value{}, err
		}
		rt.context = contextExpression
	}

	Root := t.Root
	for t.extends != nil {
		t = t.extends
		Root = t.Root
	}

	return rt.executeList(Root)
}

var (
	valueBoolTRUE  = reflect.ValueOf(true)
	valueBoolFALSE = reflect.ValueOf(false)
)

func (rt *Runtime) evalPrimaryExpressionGroup(node Expression) (reflect.Value, e.Error) {
	switch node.Type() {
	case NodeAdditiveExpr:
		return rt.evalAdditiveExpression(node.(*AdditiveExprNode))
	case NodeMultiplicativeExpr:
		return rt.evalMultiplicativeExpression(node.(*MultiplicativeExprNode))
	case NodeComparativeExpr:
		return rt.evalComparativeExpression(node.(*ComparativeExprNode))
	case NodeNumericComparativeExpr:
		return rt.evalNumericComparativeExpression(node.(*NumericComparativeExprNode))
	case NodeLogicalExpr:
		return rt.evalLogicalExpression(node.(*LogicalExprNode))
	case NodeNotExpr:
		notExpression, err := rt.evalPrimaryExpressionGroup(node.(*NotExprNode).Expr)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(!isTrue(notExpression)), nil
	case NodeTernaryExpr:
		node := node.(*TernaryExprNode)
		booleanExpression, err := rt.evalPrimaryExpressionGroup(node.Boolean)
		if err != nil {
			return reflect.Value{}, err
		}
		if isTrue(booleanExpression) {
			return rt.evalPrimaryExpressionGroup(node.Left)
		}
		return rt.evalPrimaryExpressionGroup(node.Right)
	case NodeCallExpr:
		node := node.(*CallExprNode)
		baseExpr, err := rt.evalBaseExpressionGroup(node.BaseExpr)
		if err != nil {
			return reflect.Value{}, err
		}
		if baseExpr.Kind() != reflect.Func {
			return reflect.Value{}, node.error("invalid.node", fmt.Sprintf("node %q is not func kind %q", node.BaseExpr, baseExpr.Type()))
		}
		ret, err := rt.evalCallExpression(baseExpr, node.CallArgs)
		if err != nil {
			return reflect.Value{}, node.error("", err.Error())
		}
		return ret, nil
	case NodeIndexExpr:
		node := node.(*IndexExprNode)
		base, err := rt.evalPrimaryExpressionGroup(node.Base)
		if err != nil {
			return reflect.Value{}, err
		}
		index, err := rt.evalPrimaryExpressionGroup(node.Index)
		if err != nil {
			return reflect.Value{}, err
		}

		resolved, err := resolveIndex(base, index, "", node.Nullable)
		if err != nil {
			return reflect.Value{}, node.error(err.Reason(), err.Message())
		}
		return resolved, nil
	case NodeSliceExpr:
		node := node.(*SliceExprNode)
		baseExpression, err := rt.evalPrimaryExpressionGroup(node.Base)
		if err != nil {
			return reflect.Value{}, err
		}

		var index, length int
		if node.Index != nil {
			indexExpression, err := rt.evalPrimaryExpressionGroup(node.Index)
			if err != nil {
				return reflect.Value{}, err
			}
			if canNumber(indexExpression.Kind()) {
				index = int(castInt64(indexExpression))
			} else {
				return reflect.Value{}, node.Index.error(e.InvalidValueReason, fmt.Sprintf("non numeric value in index expression kind %s", indexExpression.Kind().String()))
			}
		}

		if node.EndIndex != nil {
			indexExpression, err := rt.evalPrimaryExpressionGroup(node.EndIndex)
			if err != nil {
				return reflect.Value{}, err
			}
			if canNumber(indexExpression.Kind()) {
				length = int(castInt64(indexExpression))
			} else {
				return reflect.Value{}, node.EndIndex.error(e.InvalidValueReason, fmt.Sprintf("non numeric value in index expression kind %s", indexExpression.Kind().String()))
			}
		} else {
			length = baseExpression.Len()
		}

		return baseExpression.Slice(index, length), nil
	}
	return rt.evalBaseExpressionGroup(node)
}

// notNil returns false when v.IsValid() == false
// or when v's kind can be nil and v.IsNil() == true
func notNil(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return !v.IsNil()
	default:
		return true
	}
}

func (rt *Runtime) isSet(node Node) (ok bool, err e.Error) {
	defer func() {
		if r := recover(); r != nil {
			// something panicked while evaluating node
			ok = false
		}
	}()

	nodeType := node.Type()

	switch nodeType {
	case NodeIndexExpr:
		node := node.(*IndexExprNode)
		isSetBase, err := rt.isSet(node.Base)
		if err != nil {
			return false, node.error("", err.Error())
		}
		isSetIndex, err := rt.isSet(node.Index)
		if err != nil {
			return false, node.error("", err.Error())
		}
		if !isSetBase || !isSetIndex {
			return false, nil
		}

		base, err := rt.evalPrimaryExpressionGroup(node.Base)
		if err != nil {
			return false, err
		}
		index, err := rt.evalPrimaryExpressionGroup(node.Index)
		if err != nil {
			return false, err
		}

		resolved, err := resolveIndex(base, index, "", node.Nullable)
		return err == nil && notNil(resolved), nil
	case NodeIdentifier:
		value, err := rt.resolve(node.String())
		return err == nil && notNil(value), nil
	case NodeField:
		node := node.(*FieldNode)
		resolved := rt.context
		for i := 0; i < len(node.Idents); i++ {
			var err error
			resolved, err = resolveIndex(resolved, reflect.Value{}, node.Idents[i].name, node.Idents[i].lax)
			if err != nil || !notNil(resolved) {
				return false, nil
			}
		}
	case NodeChain:
		node := node.(*ChainNode)
		resolved, err := rt.evalChainNodeExpression(node)
		return err == nil && notNil(resolved), nil
	default:
		// todo: maybe work some edge cases
		if !(nodeType > beginExpressions && nodeType < endExpressions) {
			return false, node.error(e.UnexpectedNodeReason, fmt.Sprintf("unexpected %q node in isset clause", node))
		}
	}
	return true, nil
}

func (rt *Runtime) evalNumericComparativeExpression(node *NumericComparativeExprNode) (reflect.Value, e.Error) {
	left, err := rt.evalPrimaryExpressionGroup(node.Left)
	if err != nil {
		return reflect.Value{}, err
	}
	right, err := rt.evalPrimaryExpressionGroup(node.Right)
	if err != nil {
		return reflect.Value{}, err
	}
	isTrue := false
	kind := left.Kind()

	// if the left value is not a float and the right is, we need to promote the left value to a float before the calculation
	// this is necessary for expressions like 4*1.23
	needFloatPromotion := !isFloat(kind) && isFloat(right.Kind())

	switch node.Operator.typ {
	case itemGreat:
		if isInt(kind) {
			if needFloatPromotion {
				isTrue = float64(left.Int()) > right.Float()
			} else {
				isTrue = left.Int() > toInt(right)
			}
		} else if isFloat(kind) {
			isTrue = left.Float() > toFloat(right)
		} else if isUint(kind) {
			if needFloatPromotion {
				isTrue = float64(left.Uint()) > right.Float()
			} else {
				isTrue = left.Uint() > toUint(right)
			}
		} else {
			return reflect.Value{}, node.Left.error(e.InvalidValueReason, "a non numeric value in numeric comparative expression")
		}
	case itemGreatEquals:
		if isInt(kind) {
			if needFloatPromotion {
				isTrue = float64(left.Int()) >= right.Float()
			} else {
				isTrue = left.Int() >= toInt(right)
			}
		} else if isFloat(kind) {
			isTrue = left.Float() >= toFloat(right)
		} else if isUint(kind) {
			if needFloatPromotion {
				isTrue = float64(left.Uint()) >= right.Float()
			} else {
				isTrue = left.Uint() >= toUint(right)
			}
		} else {
			return reflect.Value{}, node.Left.error(e.InvalidValueReason, "a non numeric value in numeric comparative expression")
		}
	case itemLess:
		if isInt(kind) {
			if needFloatPromotion {
				isTrue = float64(left.Int()) < right.Float()
			} else {
				isTrue = left.Int() < toInt(right)
			}
		} else if isFloat(kind) {
			isTrue = left.Float() < toFloat(right)
		} else if isUint(kind) {
			if needFloatPromotion {
				isTrue = float64(left.Uint()) < right.Float()
			} else {
				isTrue = left.Uint() < toUint(right)
			}
		} else {
			return reflect.Value{}, node.Left.error(e.InvalidValueReason, "a non numeric value in numeric comparative expression")
		}
	case itemLessEquals:
		if isInt(kind) {
			if needFloatPromotion {
				isTrue = float64(left.Int()) <= right.Float()
			} else {
				isTrue = left.Int() <= toInt(right)
			}
		} else if isFloat(kind) {
			isTrue = left.Float() <= toFloat(right)
		} else if isUint(kind) {
			if needFloatPromotion {
				isTrue = float64(left.Uint()) <= right.Float()
			} else {
				isTrue = left.Uint() <= toUint(right)
			}
		} else {
			return reflect.Value{}, node.Left.error(e.InvalidValueReason, "a non numeric value in numeric comparative expression")
		}
	}
	return reflect.ValueOf(isTrue), nil
}

func (rt *Runtime) evalLogicalExpression(node *LogicalExprNode) (reflect.Value, e.Error) {
	left, err := rt.evalPrimaryExpressionGroup(node.Left)
	if err != nil {
		return reflect.Value{}, err
	}
	truthy := isTrue(left)
	right, err := rt.evalPrimaryExpressionGroup(node.Right)
	if err != nil {
		return reflect.Value{}, err
	}
	if node.Operator.typ == itemAnd {
		truthy = truthy && isTrue(right)
	} else {
		truthy = truthy || isTrue(right)
	}
	return reflect.ValueOf(truthy), nil
}

func (rt *Runtime) evalComparativeExpression(node *ComparativeExprNode) (reflect.Value, e.Error) {
	left, err := rt.evalPrimaryExpressionGroup(node.Left)
	if err != nil {
		return reflect.Value{}, err
	}
	right, err := rt.evalPrimaryExpressionGroup(node.Right)
	if err != nil {
		return reflect.Value{}, err
	}
	equal := checkEquality(left, right)
	if node.Operator.typ == itemNotEquals {
		return reflect.ValueOf(!equal), nil
	}
	return reflect.ValueOf(equal), nil
}

func toInt(v reflect.Value) int64 {
	if !v.IsValid() {
		panic(e.InvalidValueErr.WithMessage("invalid value can't be converted to int64"))
	}
	kind := v.Kind()
	if isInt(kind) {
		return v.Int()
	} else if isFloat(kind) {
		return int64(v.Float())
	} else if isUint(kind) {
		return int64(v.Uint())
	} else if kind == reflect.String {
		n, err := strconv.ParseInt(v.String(), 10, 0)
		if err != nil {
			panic(e.New().WithReason("invalid.parse").WithMessage(err.Error()))
		}
		return n
	} else if kind == reflect.Bool {
		if v.Bool() {
			return 0
		}
		return 1
	}
	panic(e.New().WithReason("invalid.type").
		WithMessage(fmt.Sprintf("type: %q can't be converted to int64", v.Type())),
	)
}

func toUint(v reflect.Value) uint64 {
	if !v.IsValid() {
		panic(e.InvalidValueErr.WithMessage("invalid value can't be converted to uint64"))
	}
	kind := v.Kind()
	if isUint(kind) {
		return v.Uint()
	} else if isInt(kind) {
		return uint64(v.Int())
	} else if isFloat(kind) {
		return uint64(v.Float())
	} else if kind == reflect.String {
		n, err := strconv.ParseUint(v.String(), 10, 0)
		if err != nil {
			panic(e.New().WithReason("invalid.parse").WithMessage(err.Error()))
		}
		return n
	} else if kind == reflect.Bool {
		if v.Bool() {
			return 0
		}
		return 1
	}
	panic(e.New().WithReason("invalid.type").
		WithMessage(fmt.Sprintf("type: %q can't be converted to uint64", v.Type())),
	)
}

func toFloat(v reflect.Value) float64 {
	if !v.IsValid() {
		panic(e.InvalidValueErr.WithMessage("invalid value can't be converted to float64"))
	}
	kind := v.Kind()
	if isFloat(kind) {
		return v.Float()
	} else if isInt(kind) {
		return float64(v.Int())
	} else if isUint(kind) {
		return float64(v.Uint())
	} else if kind == reflect.String {
		n, err := strconv.ParseFloat(v.String(), 0)
		if err != nil {
			panic(e.New().WithReason("invalid.parse").WithMessage(err.Error()))
		}
		return n
	} else if kind == reflect.Bool {
		if v.Bool() {
			return 0
		}
		return 1
	}
	panic(e.New().WithReason("invalid.type").
		WithMessage(fmt.Sprintf("type: %q can't be converted to float64", v.Type())),
	)
}

func (rt *Runtime) evalMultiplicativeExpression(node *MultiplicativeExprNode) (reflect.Value, e.Error) {
	left, err := rt.evalPrimaryExpressionGroup(node.Left)
	if err != nil {
		return reflect.Value{}, err
	}
	right, err := rt.evalPrimaryExpressionGroup(node.Right)
	if err != nil {
		return reflect.Value{}, err
	}
	kind := left.Kind()
	// if the left value is not a float and the right is, we need to promote the left value to a float before the calculation
	// this is necessary for expressions like 4*1.23
	needFloatPromotion := !isFloat(kind) && isFloat(right.Kind())
	switch node.Operator.typ {
	case itemMul:
		if isInt(kind) {
			if needFloatPromotion {
				// do the promotion and calculates
				left = reflect.ValueOf(float64(left.Int()) * right.Float())
			} else {
				// do not need float promotion
				left = reflect.ValueOf(left.Int() * toInt(right))
			}
		} else if isFloat(kind) {
			left = reflect.ValueOf(left.Float() * toFloat(right))
		} else if isUint(kind) {
			if needFloatPromotion {
				left = reflect.ValueOf(float64(left.Uint()) * right.Float())
			} else {
				left = reflect.ValueOf(left.Uint() * toUint(right))
			}
		} else {
			return reflect.Value{}, node.Left.error(e.InvalidValueReason, "a non numeric value in multiplicative expression")
		}
	case itemDiv:
		if isInt(kind) {
			if needFloatPromotion {
				left = reflect.ValueOf(float64(left.Int()) / right.Float())
			} else {
				left = reflect.ValueOf(left.Int() / toInt(right))
			}
		} else if isFloat(kind) {
			left = reflect.ValueOf(left.Float() / toFloat(right))
		} else if isUint(kind) {
			if needFloatPromotion {
				left = reflect.ValueOf(float64(left.Uint()) / right.Float())
			} else {
				left = reflect.ValueOf(left.Uint() / toUint(right))
			}
		} else {
			return reflect.Value{}, node.Left.error(e.InvalidValueReason, "a non numeric value in multiplicative expression")
		}
	case itemMod:
		if isInt(kind) {
			left = reflect.ValueOf(left.Int() % toInt(right))
		} else if isFloat(kind) {
			left = reflect.ValueOf(int64(left.Float()) % toInt(right))
		} else if isUint(kind) {
			left = reflect.ValueOf(left.Uint() % toUint(right))
		} else {
			return reflect.Value{}, node.Left.error("invalid.value", "a non numeric value in multiplicative expression")
		}
	}
	return left, nil
}

func (rt *Runtime) evalAdditiveExpression(node *AdditiveExprNode) (reflect.Value, e.Error) {
	isAdditive := node.Operator.typ == itemAdd
	if node.Left == nil {
		right, err := rt.evalPrimaryExpressionGroup(node.Right)
		if err != nil {
			return reflect.Value{}, err
		}
		if !right.IsValid() {
			return reflect.Value{}, node.error(e.InvalidValueReason, "right side of additive expression is invalid value")
		}
		kind := right.Kind()
		// todo: optimize
		if isInt(kind) {
			if isAdditive {
				return reflect.ValueOf(+right.Int()), nil
			} else {
				return reflect.ValueOf(-right.Int()), nil
			}
		} else if isUint(kind) {
			if isAdditive {
				return right, nil
			} else {
				return reflect.ValueOf(-int64(right.Uint())), nil
			}
		} else if isFloat(kind) {
			if isAdditive {
				return reflect.ValueOf(+right.Float()), nil
			} else {
				return reflect.ValueOf(-right.Float()), nil
			}
		}
		return reflect.Value{}, node.Left.error(e.InvalidValueReason, fmt.Sprintf("additive expression: right side %s (%s) is not a numeric value (no left side)", node.Right, getTypeString(right)))
	}

	left, err := rt.evalPrimaryExpressionGroup(node.Left)
	if err != nil {
		return reflect.Value{}, err
	}
	right, err := rt.evalPrimaryExpressionGroup(node.Right)
	if err != nil {
		return reflect.Value{}, err
	}
	if !left.IsValid() {
		return reflect.Value{}, node.error(e.InvalidValueReason, "left side of additive expression is invalid value")
	}
	if !right.IsValid() {
		return reflect.Value{}, node.error(e.InvalidValueReason, "right side of additive expression is invalid value")
	}
	kind := left.Kind()
	// if the left value is not a float and the right is, we need to promote the left value to a float before the calculation
	// this is necessary for expressions like 4+1.23
	needFloatPromotion := !isFloat(kind) && kind != reflect.String && isFloat(right.Kind())
	if needFloatPromotion {
		if isInt(kind) {
			if isAdditive {
				left = reflect.ValueOf(float64(left.Int()) + right.Float())
			} else {
				left = reflect.ValueOf(float64(left.Int()) - right.Float())
			}
		} else if isUint(kind) {
			if isAdditive {
				left = reflect.ValueOf(float64(left.Uint()) + right.Float())
			} else {
				left = reflect.ValueOf(float64(left.Uint()) - right.Float())
			}
		} else {
			return reflect.Value{}, node.Left.error(e.InvalidValueReason, fmt.Sprintf("additive expression: left side (%s (%s) needs float promotion but neither int nor uint)", node.Left, getTypeString(left)))
		}
	} else {
		if isInt(kind) {
			if isAdditive {
				left = reflect.ValueOf(left.Int() + toInt(right))
			} else {
				left = reflect.ValueOf(left.Int() - toInt(right))
			}
		} else if isFloat(kind) {
			if isAdditive {
				left = reflect.ValueOf(left.Float() + toFloat(right))
			} else {
				left = reflect.ValueOf(left.Float() - toFloat(right))
			}
		} else if isUint(kind) {
			if isAdditive {
				left = reflect.ValueOf(left.Uint() + toUint(right))
			} else {
				left = reflect.ValueOf(left.Uint() - toUint(right))
			}
		} else if kind == reflect.String {
			if !isAdditive {
				return reflect.Value{}, node.Right.error("not_allowed.signal", "minus signal is not allowed with strings")
			}
			// converts []byte (and alias types of []byte) to string
			if right.Kind() == reflect.Slice && right.Type().Elem().Kind() == reflect.Uint8 {
				right = right.Convert(left.Type())
			}
			left = reflect.ValueOf(left.String() + fmt.Sprint(right))
		} else {
			return reflect.Value{}, node.Left.error(e.InvalidValueReason, fmt.Sprintf("additive expression: left side %s (%s) is not a numeric value", node.Left, getTypeString(left)))
		}
	}

	return left, nil
}

func getTypeString(value reflect.Value) string {
	if value.IsValid() {
		return value.Type().String()
	}
	return "<invalid>"
}

func (rt *Runtime) evalBaseExpressionGroup(node Node) (reflect.Value, e.Error) {
	switch node.Type() {
	case NodeNil:
		return reflect.ValueOf(nil), nil
	case NodeBool:
		if node.(*BoolNode).True {
			return valueBoolTRUE, nil
		}
		return valueBoolFALSE, nil
	case NodeString:
		return reflect.ValueOf(&node.(*StringNode).Text).Elem(), nil
	case NodeIdentifier:
		val, err := rt.resolve(node.(*IdentifierNode).Ident)
		if err != nil {
			return reflect.Value{}, node.error(err.Reason(), err.Message())
		}
		return val, nil
	case NodeField:
		node := node.(*FieldNode)
		resolved := rt.context
		for i := 0; i < len(node.Idents); i++ {
			field, err := resolveIndex(resolved, reflect.Value{}, node.Idents[i].name, node.Idents[i].lax)
			if err != nil {
				return reflect.Value{}, node.error(err.Reason(), err.Message())
			}
			if !field.IsValid() {
				return reflect.Value{}, node.error(e.NotFoundFieldOrMethodReason, fmt.Sprintf("there is no field or method '%s' in %s (.%s)", node.Idents[i].name, getTypeString(resolved), strings.Join(node.Idents.names(), ".")))
			}
			resolved = field
		}
		return resolved, nil
	case NodeChain:
		resolved, err := rt.evalChainNodeExpression(node.(*ChainNode))
		if err != nil {
			return reflect.Value{}, node.error(err.Reason(), err.Message())
		}
		return resolved, nil
	case NodeNumber:
		node := node.(*NumberNode)
		if node.IsFloat {
			return reflect.ValueOf(&node.Float64).Elem(), nil
		}

		if node.IsInt {
			return reflect.ValueOf(&node.Int64).Elem(), nil
		}

		if node.IsUint {
			return reflect.ValueOf(&node.Uint64).Elem(), nil
		}
	}
	return reflect.Value{}, node.error(e.UnexpectedNodeTypeReason, fmt.Sprintf("unexpected node type %s in unary expression evaluating", node))
}

func (rt *Runtime) evalCallExpression(baseExpr reflect.Value, args CallArgs) (reflect.Value, e.Error) {
	return rt.evalPipeCallExpression(baseExpr, args, nil)
}

func (rt *Runtime) evalPipeCallExpression(baseExpr reflect.Value, args CallArgs, pipedArg *reflect.Value) (reflect.Value, e.Error) {
	if !baseExpr.IsValid() {
		return reflect.Value{}, e.New().
			WithReason("invalid.value").
			WithMessage("base of call expression is invalid value")
	}
	if funcType.AssignableTo(baseExpr.Type()) {
		return baseExpr.Interface().(Func)(Arguments{runtime: rt, args: args, pipedVal: pipedArg}), nil
	}

	argValues, err := rt.evaluateArgs(baseExpr.Type(), args, pipedArg)
	if err != nil {
		return reflect.Value{}, e.New().
			WithReason("invalid.call").
			WithMessage(fmt.Sprintf("call expression: %v", err))
	}

	returns := baseExpr.Call(argValues)
	if len(returns) == 0 {
		return reflect.Value{}, nil
	}

	return returns[0], nil
}

func (rt *Runtime) evalCommandExpression(node *CommandNode) (reflect.Value, bool, e.Error) {
	term, err := rt.evalPrimaryExpressionGroup(node.BaseExpr)
	if err != nil {
		return reflect.Value{}, false, err
	}
	if term.IsValid() && node.Exprs != nil {
		if term.Kind() == reflect.Func {
			if term.Type() == safeWriterType {
				return reflect.Value{}, true, rt.evalSafeWriter(term, node)
			}
			ret, err := rt.evalCallExpression(term, node.CallArgs)
			if err != nil {
				return reflect.Value{}, false, node.BaseExpr.error("", err.Error())
			}
			return ret, false, nil
		}
		return reflect.Value{}, false, node.Exprs[0].error("", fmt.Sprintf("command %q has arguments but is %s, not a function", node.Exprs[0], term.Type()))
	}
	return term, false, nil
}

func (rt *Runtime) evalChainNodeExpression(node *ChainNode) (reflect.Value, e.Error) {
	resolved, err := rt.evalPrimaryExpressionGroup(node.Node)
	if err != nil {
		return reflect.Value{}, err
	}

	for i := 0; i < len(node.Field); i++ {
		lax := node.Field[i].lax
		field, err := resolveIndex(resolved, reflect.ValueOf(node.Field[i].name), node.Field[i].name, lax)
		if err != nil {
			return reflect.Value{}, node.error(err.Reason(), err.Message())
		}
		if !field.IsValid() {
			if resolved.Kind() == reflect.Map && i == len(node.Field)-1 {
				// return reflect.Zero(resolved.Type().Elem()), nil
				return reflect.Value{}, nil
			}
			if !lax {
				return reflect.Value{}, e.New().
					WithReason(e.NotFoundFieldOrMethodReason).
					WithMessage(fmt.Sprintf("there is no field or method '%s' in %s (%s)", node.Field[i].name, getTypeString(resolved), node))
			}
			field = reflect.ValueOf(nil)
		}
		resolved = field
	}

	return resolved, nil
}

type escapeWriter struct {
	rawWriter  io.Writer
	safeWriter SafeWriter
}

func (w *escapeWriter) Write(b []byte) (int, error) {
	w.safeWriter(w.rawWriter, b)
	return 0, nil
}

func (rt *Runtime) evalSafeWriter(term reflect.Value, node *CommandNode, v ...reflect.Value) e.Error {
	sw := &escapeWriter{rawWriter: rt.Writer, safeWriter: term.Interface().(SafeWriter)}
	for i := 0; i < len(v); i++ {
		if _, err := fastprinter.PrintValue(sw, v[i]); err != nil {
			return e.New().WithReason("invalid.arguments").WithMessage(err.Error())
		}
	}
	for i := 0; i < len(node.Exprs); i++ {
		expression, err := rt.evalPrimaryExpressionGroup(node.Exprs[i])
		if err != nil {
			return err
		}
		if _, err := fastprinter.PrintValue(sw, expression); err != nil {
			return e.New().WithReason("invalid.arguments").WithMessage(err.Error())
		}
	}

	return nil
}

func (rt *Runtime) evalCommandPipeExpression(node *CommandNode, value reflect.Value) (reflect.Value, bool, e.Error) {
	term, err := rt.evalPrimaryExpressionGroup(node.BaseExpr)
	if err != nil {
		return reflect.Value{}, false, err
	}
	if !term.IsValid() {
		return reflect.Value{}, false, node.error(e.InvalidValueReason, "base expression of command pipe node is invalid value")
	}
	if term.Kind() != reflect.Func {
		return reflect.Value{}, false, node.BaseExpr.error("", fmt.Sprintf("pipe command %q must be a function, but is %s", node.BaseExpr, term.Type()))
	}

	if term.Type() == safeWriterType {
		return reflect.Value{}, true, rt.evalSafeWriter(term, node, value)
	}

	ret, err := rt.evalPipeCallExpression(term, node.CallArgs, &value)
	if err != nil {
		return reflect.Value{}, false, node.BaseExpr.error("", err.Error())
	}
	return ret, false, nil
}

func (rt *Runtime) evalPipelineExpression(node *PipeNode) (value reflect.Value, safeWriter bool, err e.Error) {
	value, safeWriter, err = rt.evalCommandExpression(node.Cmds[0])
	if err != nil {
		return reflect.Value{}, false, err
	}
	for i := 1; i < len(node.Cmds); i++ {
		if safeWriter {
			return reflect.Value{}, false, node.Cmds[i].error(e.UnexpectedCommandReason, fmt.Sprintf("unexpected command %s, writer command should be the last command", node.Cmds[i]))
		}
		value, safeWriter, err = rt.evalCommandPipeExpression(node.Cmds[i], value)
	}
	return
}

func (rt *Runtime) evaluateArgs(fnType reflect.Type, args CallArgs, pipedArg *reflect.Value) ([]reflect.Value, e.Error) {
	numArgs := len(args.Exprs)
	if !args.HasPipeSlot && pipedArg != nil {
		numArgs++
	}
	numArgsRequired := fnType.NumIn()
	isVariadic := fnType.IsVariadic()
	invalidNumOfArgsError := e.New().
		WithReason(e.InvalidNumberOfArgumentsReason).
		WithMessage(fmt.Sprintf("%s needs at least %d arguments, but have %d", fnType, numArgsRequired, numArgs))
	if isVariadic {
		numArgsRequired--
		if numArgs < numArgsRequired {
			return nil, invalidNumOfArgsError
		}
	} else {
		if numArgs != numArgsRequired {
			return nil, invalidNumOfArgsError
		}
	}

	argValues := make([]reflect.Value, numArgs)
	slot := 0 // index in argument values (evaluated expressions combined with piped argument if applicable)

	if !args.HasPipeSlot && pipedArg != nil {
		in := fnType.In(slot)
		if !(*pipedArg).IsValid() {
			return nil, e.InvalidValueErr.
				WithMessage(fmt.Sprintf("piped first argument for %s is not a valid value", fnType))
		}
		if !(*pipedArg).Type().AssignableTo(in) {
			*pipedArg = (*pipedArg).Convert(in)
		}
		argValues[slot] = *pipedArg
		slot++
	}

	i := 0 // index in parsed argument expression list

	invalidArgError := e.InvalidValueErr.
		WithMessage(fmt.Sprintf("argument for position %d in %s is not a valid value", slot, fnType))

	var err e.Error
	for slot < numArgsRequired {
		in := fnType.In(slot)
		var term reflect.Value
		if args.Exprs[i].Type() == NodeUnderscore {
			term = *pipedArg
		} else {
			term, err = rt.evalPrimaryExpressionGroup(args.Exprs[i])
			if err != nil {
				return nil, err
			}
		}
		if !term.IsValid() {
			return nil, invalidArgError
		}
		if !term.Type().AssignableTo(in) {
			term = term.Convert(in)
		}
		argValues[slot] = term
		i++
		slot++
	}

	if isVariadic {
		in := fnType.In(numArgsRequired).Elem()
		for i < len(args.Exprs) {
			var term reflect.Value
			if args.Exprs[i].Type() == NodeUnderscore {
				term = *pipedArg
			} else {
				term, err = rt.evalPrimaryExpressionGroup(args.Exprs[i])
				if err != nil {
					return nil, err
				}
			}
			if !term.IsValid() {
				return nil, invalidArgError
			}
			if !term.Type().AssignableTo(in) {
				term = term.Convert(in)
			}
			argValues[slot] = term
			i++
			slot++
		}
	}

	return argValues, nil
}

func isUint(kind reflect.Kind) bool {
	return kind >= reflect.Uint && kind <= reflect.Uint64
}

func isInt(kind reflect.Kind) bool {
	return kind >= reflect.Int && kind <= reflect.Int64
}

func isFloat(kind reflect.Kind) bool {
	return kind == reflect.Float32 || kind == reflect.Float64
}

// checkEquality of two reflect values in the semantic of the jet runtime
func checkEquality(v1, v2 reflect.Value) bool {
	v1 = indirectInterface(v1)
	v2 = indirectInterface(v2)

	if !v1.IsValid() || !v2.IsValid() {
		return v1.IsValid() == v2.IsValid()
	}

	v1Type := v1.Type()
	v2Type := v2.Type()

	// fast path
	if v1Type != v2Type && !v2Type.AssignableTo(v1Type) && !v2Type.ConvertibleTo(v1Type) {
		return false
	}

	kind := v1.Kind()
	if isInt(kind) {
		return v1.Int() == toInt(v2)
	}
	if isFloat(kind) {
		return v1.Float() == toFloat(v2)
	}
	if isUint(kind) {
		return v1.Uint() == toUint(v2)
	}

	switch kind {
	case reflect.Bool:
		return v1.Bool() == isTrue(v2)
	case reflect.String:
		return v1.String() == v2.String()
	case reflect.Array:
		vlen := v1.Len()
		if vlen == v2.Len() {
			return false
		}
		for i := 0; i < vlen; i++ {
			if !checkEquality(v1.Index(i), v2.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if v1.IsNil() != v2.IsNil() {
			return false
		}

		vlen := v1.Len()
		if vlen != v2.Len() {
			return false
		}

		if v1.CanAddr() && v2.CanAddr() && v1.Pointer() == v2.Pointer() {
			return true
		}

		for i := 0; i < vlen; i++ {
			if !checkEquality(v1.Index(i), v2.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Interface:
		if v1.IsNil() || v2.IsNil() {
			return v1.IsNil() == v2.IsNil()
		}
		return checkEquality(v1.Elem(), v2.Elem())
	case reflect.Ptr:
		return v1.Pointer() == v2.Pointer()
	case reflect.Struct:
		numField := v1.NumField()
		for i, n := 0, numField; i < n; i++ {
			if !checkEquality(v1.Field(i), v2.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if v1.IsNil() != v2.IsNil() {
			return false
		}
		if v1.Len() != v2.Len() {
			return false
		}
		if v1.Pointer() == v2.Pointer() {
			return true
		}
		for _, k := range v1.MapKeys() {
			val1 := v1.MapIndex(k)
			val2 := v2.MapIndex(k)
			if !val1.IsValid() || !val2.IsValid() || !checkEquality(v1.MapIndex(k), v2.MapIndex(k)) {
				return false
			}
		}
		return true
	case reflect.Func:
		return v1.IsNil() && v2.IsNil()
	default:
		// Normal equality suffices
		return v1.Interface() == v2.Interface()
	}
}

func isTrue(v reflect.Value) bool {
	return v.IsValid() && !v.IsZero()
}

func canNumber(kind reflect.Kind) bool {
	return isInt(kind) || isUint(kind) || isFloat(kind)
}

func castInt64(v reflect.Value) int64 {
	kind := v.Kind()
	switch {
	case isInt(kind):
		return v.Int()
	case isUint(kind):
		return int64(v.Uint())
	case isFloat(kind):
		return int64(v.Float())
	}
	return 0
}

var (
	cachedStructsMutex      = sync.RWMutex{}
	cachedStructsFieldIndex = map[reflect.Type]map[string][]int{}
)

// from text/template's exec.go:
//
// indirect returns the item at the end of indirection, and a bool to indicate
// if it's nil. If the returned bool is true, the returned value's kind will be
// either a pointer or interface.
func indirect(v reflect.Value) (rv reflect.Value, isNil bool) {
	for ; v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface; v = v.Elem() {
		if v.IsNil() {
			return v, true
		}
	}
	return v, false
}

// indirectInterface returns the concrete value in an interface value, or else v itself.
// That is, if v represents the interface value x, the result is the same as reflect.ValueOf(x):
// the fact that x was an interface value is forgotten.
func indirectInterface(v reflect.Value) reflect.Value {
	if v.Kind() == reflect.Interface {
		return v.Elem()
	}
	return v
}

// indirectEface is the same as indirectInterface, but only indirects through v if its type
// is the empty interface and its value is not nil.
func indirectEface(v reflect.Value) reflect.Value {
	if v.Kind() == reflect.Interface && v.Type().NumMethod() == 0 && !v.IsNil() {
		return v.Elem()
	}
	return v
}

// mostly copied from text/template's evalField() (exec.go):
//
// The index to use to access v can be specified in either index or indexAsStr.
// Which parameter is filled depends on the call path up to when a particular
// call to resolveIndex is made and whether that call site already has access
// to a reflect.Value for the index or just a string identifier.
//
// While having both options makes the implementation of this function more
// complex, it improves the memory allocation story for the most common
// execution paths when executing a template, such as when accessing a field
// element.
func resolveIndex(v, index reflect.Value, indexAsStr string, lax bool) (reflect.Value, e.Error) {
	if !v.IsValid() {
		if lax {
			return reflect.Value{}, nil
		}
		return reflect.Value{}, e.New().
			WithReason(e.NotFoundFieldOrMethodReason).
			WithMessage(fmt.Sprintf("there is no field or method '%s' in nil", indexAsStr))
	}

	v, isNil := indirect(v)
	if v.Kind() == reflect.Interface && isNil {
		// Calling a method on a nil interface can't work. The
		// MethodByName method call below would panic.
		return reflect.Value{}, e.InvalidValueErr.
			WithMessage(fmt.Sprintf("nil pointer evaluating %s.%s", v.Type(), index))
	}

	// Handle the caller passing either index or indexAsStr.
	indexIsStr := indexAsStr != ""
	indexAsValue := func() reflect.Value { return index }
	if indexIsStr {
		// indexAsStr was specified, so make the indexAsValue function
		// obtain the corresponding reflect.Value. This is only used in
		// some code paths, and since it causes an allocation, a
		// function is used instead of always extracting the
		// reflect.Value.
		indexAsValue = func() reflect.Value {
			return reflect.ValueOf(indexAsStr)
		}
	} else {
		// index was specified, so extract the string value if the index
		// is in fact a string.
		indexIsStr = index.Kind() == reflect.String
		if indexIsStr {
			indexAsStr = index.String()
		}
	}

	// Unless it's an interface, need to get to a value of type *T to guarantee
	// we see all methods of T and *T.
	if indexIsStr {
		ptr := v
		if ptr.Kind() != reflect.Interface && ptr.Kind() != reflect.Ptr && ptr.CanAddr() {
			ptr = ptr.Addr()
		}
		if method := ptr.MethodByName(indexAsStr); method.IsValid() {
			return method, nil
		}
	}

	// It's not a method on v; so now:
	//  - if v is array/slice/string, use index as numeric index
	//  - if v is a struct, use index as field name
	//  - if v is a map, use index as key
	//  - if v is (still) a pointer, indexing will fail but we check for nil to get a useful error
	switch v.Kind() {
	case reflect.Array, reflect.Slice, reflect.String:
		indexVal := indexAsValue()
		x, err := indexArg(indexVal, v.Len())
		if err != nil {
			if lax {
				return reflect.Value{}, nil
			}
			return reflect.Value{}, err
		}
		return indirectEface(v.Index(x)), nil
	case reflect.Struct:
		if !indexIsStr {
			return reflect.Value{}, e.InvalidIndexErr.
				WithMessage(fmt.Sprintf("can't use '%v' (%s, not string) as field name in struct type %s", index, indexAsValue().Type(), v.Type())).
				WithDetail("object", v.String()).
				WithDetail("index", fmt.Sprintf("%v", index))
		}
		typ := v.Type()
		key := indexAsStr

		// Fast path: use the struct cache to avoid allocations.
		cachedStructsMutex.RLock()
		cache, ok := cachedStructsFieldIndex[typ]
		cachedStructsMutex.RUnlock()
		if !ok {
			cachedStructsMutex.Lock()
			if cache, ok = cachedStructsFieldIndex[typ]; !ok {
				cache = make(map[string][]int)
				buildCache(typ, cache, nil)
				cachedStructsFieldIndex[typ] = cache
			}
			cachedStructsMutex.Unlock()
		}
		if id, ok := cache[key]; ok {
			return v.FieldByIndex(id), nil
		}

		// Slow path: use reflect directly
		tField, ok := typ.FieldByName(key)
		if ok {
			field := v.FieldByIndex(tField.Index)
			if tField.PkgPath != "" { // field is unexported
				return reflect.Value{}, e.InvalidIndexErr.
					WithMessage(fmt.Sprintf("%s is an unexported field of struct type %s", indexAsStr, v.Type())).
					WithDetail("object", v.String()).
					WithDetail("index", fmt.Sprintf("%v", index))
			}
			return indirectEface(field), nil
		}
		if lax {
			return reflect.Value{}, nil
		}
		return reflect.Value{}, e.InvalidIndexErr.
			WithMessage(fmt.Sprintf("can't use '%s' as field name in struct type %s", indexAsStr, v.Type())).
			WithDetail("object", v.String()).
			WithDetail("index", fmt.Sprintf("%v", index))
	case reflect.Map:
		// If it's a map, attempt to use the field name as a key.
		indexVal := indexAsValue()
		if !indexVal.Type().ConvertibleTo(v.Type().Key()) {
			return reflect.Value{}, e.InvalidIndexErr.
				WithMessage(fmt.Sprintf("can't use '%s' (%s) as key for map of type %s", indexAsStr, indexVal.Type(), v.Type())).
				WithDetail("object", v.String()).
				WithDetail("index", fmt.Sprintf("%v", index))
		}
		index = indexVal.Convert(v.Type().Key()) // noop in most cases, but not expensive
		return indirectEface(v.MapIndex(indexVal)), nil
	case reflect.Ptr:
		etyp := v.Type().Elem()
		if etyp.Kind() == reflect.Struct && indexIsStr {
			if _, ok := etyp.FieldByName(indexAsStr); !ok {
				// If there's no such field, say "can't evaluate"
				// instead of "nil pointer evaluating".
				break
			}
		}
		if isNil {
			return reflect.Value{}, e.InvalidValueErr.
				WithMessage(fmt.Sprintf("nil pointer evaluating %s.%s", v.Type(), index))
		}
	}
	if lax {
		return reflect.Value{}, nil
	}
	return reflect.Value{}, e.InvalidIndexErr.
		WithMessage(fmt.Sprintf("can't evaluate index %s (%s) in type %s", index, indexAsStr, getTypeString(v)))
}

// from Go's text/template's funcs.go:
//
// indexArg checks if a reflect.Value can be used as an index, and converts it to int if possible.
func indexArg(index reflect.Value, cap int) (int, e.Error) {
	var x int64
	switch index.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		x = index.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		x = int64(index.Uint())
	case reflect.Float32, reflect.Float64:
		x = int64(index.Float())
	case reflect.Invalid:
		return 0, e.InvalidIndexErr.WithMessage("cannot index slice/array/string with nil")
	default:
		return 0, e.InvalidIndexErr.
			WithMessage(fmt.Sprintf("cannot index slice/array/string with type %s", getTypeString(index)))
	}
	if int(x) < 0 || int(x) >= cap {
		return 0, e.InvalidIndexErr.WithMessage(fmt.Sprintf("index out of range: %d", x))
	}
	return int(x), nil
}

func buildCache(typ reflect.Type, cache map[string][]int, parent []int) {
	numFields := typ.NumField()
	max := len(parent) + 1

	for i := 0; i < numFields; i++ {

		index := make([]int, max)
		copy(index, parent)
		index[len(parent)] = i

		field := typ.Field(i)
		if field.Anonymous {
			typ := field.Type
			if typ.Kind() == reflect.Struct {
				buildCache(typ, cache, index)
			}
		}
		cache[field.Name] = index
	}
}
