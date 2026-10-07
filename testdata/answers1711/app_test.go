package app

import "testing"

func TestUpdate(t *testing.T)      { Update() }
func TestOtherUpdate(t *testing.T) { Handler(nil, nil) }

type FakeResource struct{}

func (*FakeResource) Lifetime() int { return 60 }
