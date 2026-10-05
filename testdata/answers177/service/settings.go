package service

import (
	"errors"
	"os"
)

var ErrLoginRateLimited = errors.New("rate limited")

func IsLimited(err error) bool { return errors.Is(err, ErrLoginRateLimited) }

func Shadow() error {
	ErrLoginRateLimited := errors.New("local")
	return ErrLoginRateLimited
}

const privateIssuer = "IDENTUUM_IDP_TEST_ALLOW_PRIVATE_UPSTREAM_ISSUER"

func AllowMultiReplica(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv("IDENTUUM_IDP_ALLOW_MULTI_REPLICA") == "true"
}

func envBool(getenv func(string) string, key string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv(key) == "true"
}

func PrivateIssuer(getenv func(string) string) bool { return envBool(getenv, privateIssuer) }

func NotEnvironment(getenv func(string) string) string { return getenv("NOT_AN_ENV") }

type Router struct{}

func (*Router) GET(path string, handler func()) {}

func healthHandler() func() { return func() {} }

func Routes(router *Router) { router.GET("/health", healthHandler()) }
