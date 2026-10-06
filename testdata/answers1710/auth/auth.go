package auth

const AuthenticatedClientKindAPIResource = "resource"

type RepositoryVerifier struct{}

func (*RepositoryVerifier) IntrospectToken() string {
	return AuthenticatedClientKindAPIResource
}
