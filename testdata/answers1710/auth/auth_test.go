package auth

import "testing"

func TestIntrospect(t *testing.T) {
	v := &RepositoryVerifier{}
	if v.IntrospectToken() != "resource" {
		t.Fatal("unexpected kind")
	}
}
