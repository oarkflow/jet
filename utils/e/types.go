package e

type Reason = string

const (
	TemplateErrorReason Reason = "jet.template.error"
	RuntimeErrorReason  Reason = "jet.runtime.error"

	InvalidValueReason             Reason = "invalid.value"
	InvalidIndexReason             Reason = "invalid.index"
	InvalidNumberOfArgumentsReason Reason = "invalid.number_of_arguments"

	UnexpectedReason               Reason = "unexpected"
	UnexpectedKeywordReason        Reason = "unexpected.keyword"
	UnexpectedTokenReason          Reason = "unexpected.token"
	UnexpectedNodeReason           Reason = "unexpected.node"
	UnexpectedNodeTypeReason       Reason = "unexpected.node.type"
	UnexpectedExpressionTypeReason Reason = "unexpected.expression.type"
	UnexpectedCommandReason        Reason = "unexpected.command"
	UnexpectedClauseReason         Reason = "unexpected.clause"

	NotFoundFieldOrMethodReason Reason = "not_found.field_or_method"
)

type (
	Template = string
	Message  = string
	Line     = int
	Column   = int
	Details  = map[string]interface{}
)
