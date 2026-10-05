package service

import "testing"

type harness struct {
	login    *OIDCLoginService
	callback *OIDCCallbackService
}

func TestFieldLogin(t *testing.T) {
	h := harness{login: &OIDCLoginService{}}
	h.login.InitiateLogin()
}

func TestFieldCallback(t *testing.T) {
	h := harness{callback: &OIDCCallbackService{}}
	h.callback.HandleCallback()
}
