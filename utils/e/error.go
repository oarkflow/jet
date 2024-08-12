package e

type Error interface {
	error

	Reason() Reason
	WithReason(Reason) Error
	CompleteReason(Reason) Error

	Message() Message
	WithMessage(Message) Error

	Position() *Position
	WithPosition(Line, Column) Error

	Details() Details
	WithDetail(string, string) Error
	WithDetails(Details) Error
}
