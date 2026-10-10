package service

import "example.com/sessions2/domain"

type I interface{ Run() }
type T struct{}

func (T) Run() {}

var _ I = (*T)(nil)
var _ I = T{}

func accepts(domain.AuthPolicy, int) {}

func Compare(org domain.Organization, number int) bool {
	return org.AuthPolicy == domain.AuthPolicyLocalOnly && number == domain.Plain
}

func Choose(policy domain.AuthPolicy, number int) bool {
	switch policy {
	case domain.AuthPolicyLocalOnly:
		return true
	}
	switch number {
	case domain.Plain:
		return true
	}
	return false
}

func Construct() (domain.Organization, []int) {
	return domain.Organization{AuthPolicy: domain.AuthPolicyLocalOnly}, []int{domain.Plain}
}

func Argument() { accepts(domain.AuthPolicyLocalOnly, domain.Plain) }

func Shadow() int {
	AuthPolicyLocalOnly := 99
	Plain := 100
	return AuthPolicyLocalOnly + Plain
}
