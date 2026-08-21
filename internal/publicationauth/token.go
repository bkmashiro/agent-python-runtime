package publicationauth

// Token is an opaque Host-internal authority to publish one fully validated
// result body. Go's internal-package rule prevents consumers outside the
// Pysolate repository from minting it.
type Token struct {
	valid bool
}

func Mint() Token {
	return Token{valid: true}
}

func (token Token) Valid() bool {
	return token.valid
}
