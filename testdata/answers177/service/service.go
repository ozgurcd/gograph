package service

type OIDCLoginService struct{}

func (*OIDCLoginService) InitiateLogin() {}

type OIDCCallbackService struct{}

func (*OIDCCallbackService) HandleCallback() {}
