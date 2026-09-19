package semantic

import passregistration "github.com/bkmashiro/agent-python-runtime/runtime/passregistration"

const (
	PassPreparedPureRegion        PassName = PassName(passregistration.PreparedPureRegion)
	PreparedPureRegionPassVersion          = passregistration.PreparedPureRegionVersion

	PassConsumerOverlayOnly    PassConsumer = passregistration.OverlayOnly
	PassConsumerExecutionPatch PassConsumer = passregistration.ExecutionPatch
)

type PassConsumer = passregistration.Consumer
type PassRegistration = passregistration.Registration

var (
	ErrInvalidPassRegistration   = passregistration.ErrInvalid
	ErrDuplicatePassRegistration = passregistration.ErrDuplicate
)

func NewPassRegistration(name PassName, version, analyzerSHA256, configSHA256 string, consumer PassConsumer) (PassRegistration, error) {
	return passregistration.New(passregistration.Name(name), version, analyzerSHA256, configSHA256, consumer)
}

type PassRegistry struct {
	registry passregistration.Registry
}

func NewPassRegistry(registrations ...PassRegistration) (PassRegistry, error) {
	registry, err := passregistration.NewRegistry(registrations...)
	return PassRegistry{registry: registry}, err
}

func (registry PassRegistry) Lookup(name PassName) (PassRegistration, bool) {
	return registry.registry.Lookup(passregistration.Name(name))
}
