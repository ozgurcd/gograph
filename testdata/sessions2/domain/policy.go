package domain

type AuthPolicy int

const (
	AuthPolicyLocalOnly AuthPolicy = iota
	AuthPolicyFederated
)

const Plain = 7

type Organization struct{ AuthPolicy AuthPolicy }
