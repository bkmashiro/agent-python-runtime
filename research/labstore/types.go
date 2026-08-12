package labstore

import (
	"errors"
	"fmt"
	"regexp"
)

const (
	ObjectSchemaVersion = "pysolate.labstore-object.v1"
	PolicySchemaVersion = "pysolate.labstore-privacy.v1"
	objectMagic         = "PYSOLATE-LABSTORE-OBJECT-V1\n"
)

var (
	ErrClosed      = errors.New("labstore is closed")
	ErrReadOnly    = errors.New("labstore is read-only")
	ErrInvalid     = errors.New("invalid labstore value")
	ErrNotFound    = errors.New("labstore object not found")
	ErrCorrupt     = errors.New("corrupt labstore object")
	ErrCredentials = errors.New("credentials are forbidden in labstore")
	ErrPrivate     = errors.New("labstore object is private")
	digestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Kind is part of content identity. Equal bytes stored for different semantic
// purposes deliberately receive different SHA-256 identities.
type Kind string

const (
	KindPrompt           Kind = "blob.prompt"
	KindProviderBody     Kind = "blob.provider-body"
	KindCode             Kind = "blob.code"
	KindToolPayload      Kind = "blob.tool-payload"
	KindFile             Kind = "blob.file"
	KindSemanticDocument Kind = "document.semantic"
	KindMetadataEvent    Kind = "event.metadata"
	KindRun              Kind = "relation.run"
	KindExecution        Kind = "relation.execution"
	KindBranch           Kind = "relation.branch"
	KindWorkspaceTree    Kind = "tree.workspace"
)

func validKind(kind Kind) bool {
	switch kind {
	case KindPrompt, KindProviderBody, KindCode, KindToolPayload, KindFile,
		KindSemanticDocument, KindMetadataEvent, KindRun, KindExecution,
		KindBranch, KindWorkspaceTree:
		return true
	default:
		return false
	}
}

var allKinds = []Kind{
	KindPrompt, KindProviderBody, KindCode, KindToolPayload, KindFile,
	KindSemanticDocument, KindMetadataEvent, KindRun, KindExecution,
	KindBranch, KindWorkspaceTree,
}

// Privacy is mutable access/export metadata and is deliberately excluded from
// immutable content identity. Private wins if classifications conflict.
type Privacy string

const (
	PrivacyPrivate  Privacy = "private"
	PrivacyPortable Privacy = "portable"
)

func validPrivacy(value Privacy) bool {
	return value == PrivacyPrivate || value == PrivacyPortable
}

// CredentialState forces callers to make the no-credential policy explicit.
// It is an admission declaration, not a claim that the store can discover all
// secrets in arbitrary bytes.
type CredentialState string

const (
	CredentialsAbsent  CredentialState = "absent"
	CredentialsPresent CredentialState = "present"
)

// Ref is a typed, domain-separated content identity.
type Ref struct {
	Kind   Kind   `json:"kind"`
	SHA256 string `json:"sha256"`
}

func (ref Ref) String() string {
	return fmt.Sprintf("%s@%s", ref.Kind, ref.SHA256)
}

func (ref Ref) validate() error {
	if !validKind(ref.Kind) || !digestPattern.MatchString(ref.SHA256) {
		return fmt.Errorf("%w: malformed content reference", ErrInvalid)
	}
	return nil
}

// Options are Host-owned ceilings. Zero values select conservative defaults.
type Options struct {
	ReadOnly            bool
	MaxObjectBytes      uint64
	MaxLinks            uint32
	MaxHeaderBytes      uint32
	MaxRoots            uint32
	MaxReachableObjects uint32
	MaxTreeEntries      uint32
}

func (options Options) normalized() Options {
	if options.MaxObjectBytes == 0 {
		options.MaxObjectBytes = 8 << 20
	}
	if options.MaxLinks == 0 {
		options.MaxLinks = 4096
	}
	if options.MaxHeaderBytes == 0 {
		options.MaxHeaderBytes = 1 << 20
	}
	if options.MaxRoots == 0 {
		options.MaxRoots = 4096
	}
	if options.MaxReachableObjects == 0 {
		options.MaxReachableObjects = 100_000
	}
	if options.MaxTreeEntries == 0 {
		options.MaxTreeEntries = 10_000
	}
	return options
}

type PutOptions struct {
	Privacy     Privacy
	Credentials CredentialState
	Links       []Ref
}

type Object struct {
	Ref     Ref
	Kind    Kind
	Privacy Privacy
	Links   []Ref
	Body    []byte
}
