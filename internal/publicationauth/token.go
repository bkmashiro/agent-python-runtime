package publicationauth

const ResultPublicationBinding = "pysolate.result-publication-authority.v1"

// Token is an opaque Host-internal seal over one validated identity. Go's
// internal-package rule prevents consumers outside the Pysolate repository
// from minting it, while binding prevents a copied token from authorizing a
// mutated Host value.
type Token struct {
	binding string
}

func Mint(binding string) Token {
	if binding == "" {
		return Token{}
	}
	return Token{binding: binding}
}

func (token Token) Valid(binding string) bool {
	return binding != "" && token.binding == binding
}
