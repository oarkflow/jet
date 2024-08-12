package e

import (
	"fmt"
)

type Builder struct {
	T Template  `json:"template,omitempty"`
	R Reason    `json:"reason,omitempty"`
	M Message   `json:"message,omitempty"`
	P *Position `json:"position,omitempty"`
	D Details   `json:"details,omitempty"`
}

type Position struct {
	L Line   `json:"line,omitempty"`
	C Column `json:"column,omitempty"`
}

func New() *Builder {
	return &Builder{}
}

func (b *Builder) Error() string {
	place := ""

	if b.T != "" {
		place = fmt.Sprintf(" %s", b.T)

		if b.P != nil {
			place = fmt.Sprintf("%s:%d:%d", place, b.P.L, b.P.C)
		}
	}

	return fmt.Sprintf(
		"%s%s %s",
		b.R, place, b.M,
	)
}

func (b *Builder) Reason() Reason {
	return b.R
}

func (b *Builder) WithReason(r Reason) Error {
	b.R = r
	return b
}

func (b *Builder) CompleteReason(r Reason) Error {
	b.R = fmt.Sprintf("%s.%s", b.R, r)
	return b
}

func (b *Builder) Message() Message {
	return b.M
}

func (b *Builder) WithMessage(m Message) Error {
	b.M = m
	return b
}

func (b *Builder) Position() *Position {
	return b.P
}

func (b *Builder) WithPosition(l Line, c Column) Error {
	b.P = &Position{L: l, C: c}
	return b
}

func (b *Builder) Details() Details {
	return b.D
}

func (b *Builder) WithDetail(k, v string) Error {
	if b.D == nil {
		b.D = make(map[string]interface{})
	}
	b.D[k] = v
	return b
}

func (b *Builder) WithDetails(d Details) Error {
	b.D = d
	return b
}

func Build(r Reason, t Template, m Message, p *Position) Error {
	return &Builder{
		R: r,
		T: t,
		M: m,
		P: p,
	}
}

var (
	InvalidValueErr = New().WithReason(InvalidValueReason)
	InvalidIndexErr = New().WithReason(InvalidIndexReason)
)
