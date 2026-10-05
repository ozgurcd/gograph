package fixture

import "testing"

func TestField(t *testing.T) {
	h := struct{ service *Service }{service: &Service{}}
	h.service.Callback()
}
