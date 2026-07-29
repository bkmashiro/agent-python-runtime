package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

var ErrInvalidProfile = errors.New("invalid workload profile")

type RequestSizeBucket string

const (
	RequestSizeTiny    RequestSizeBucket = "tiny"
	RequestSizeSmall   RequestSizeBucket = "small"
	RequestSizeMedium  RequestSizeBucket = "medium"
	RequestSizeLarge   RequestSizeBucket = "large"
	RequestSizeUnknown RequestSizeBucket = "unknown"
)

type WorkloadProfile struct {
	ArtifactDigest    string
	WorkloadClass     string
	RequestSizeBucket RequestSizeBucket
	CapabilityPattern string
	PolicyClass       string
}

func (profile WorkloadProfile) Key() (string, error) {
	if !validSHA256(profile.ArtifactDigest) || !boundedIdentifier(profile.WorkloadClass) ||
		!validRequestSizeBucket(profile.RequestSizeBucket) || !boundedIdentifier(profile.CapabilityPattern) ||
		!boundedIdentifier(profile.PolicyClass) {
		return "", ErrInvalidProfile
	}
	payload := profile.ArtifactDigest + "\x00" + profile.WorkloadClass + "\x00" + string(profile.RequestSizeBucket) + "\x00" + profile.CapabilityPattern + "\x00" + profile.PolicyClass
	digest := sha256.Sum256([]byte(payload))
	return "profile_" + hex.EncodeToString(digest[:]), nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validRequestSizeBucket(value RequestSizeBucket) bool {
	switch value {
	case RequestSizeTiny, RequestSizeSmall, RequestSizeMedium, RequestSizeLarge, RequestSizeUnknown:
		return true
	default:
		return false
	}
}

type ProfileConfig struct {
	HardBytes               uint64
	UnknownReservationBytes uint64
	PerAttemptMarginBytes   uint64
	MaxProfiles             uint32
	MaxTrackedAttempts      uint32
	MaxSamplesPerProfile    uint32
	MaxAggregateSamples     uint32
	ColdRuns                uint32
	StableSampleEvery       uint32
	MinimumSamples          uint32
	ReservationQuantileBPS  uint32
}

func (config ProfileConfig) validate() error {
	if config.HardBytes == 0 || config.UnknownReservationBytes == 0 || config.UnknownReservationBytes > config.HardBytes ||
		config.PerAttemptMarginBytes > config.HardBytes || config.MaxProfiles == 0 || config.MaxProfiles > 1<<20 ||
		config.MaxTrackedAttempts == 0 || config.MaxTrackedAttempts > 1<<22 || config.MaxSamplesPerProfile == 0 ||
		config.MaxSamplesPerProfile > 1<<16 || config.MaxAggregateSamples < config.MinimumSamples || config.MaxAggregateSamples > 1<<20 ||
		config.ColdRuns == 0 || config.StableSampleEvery == 0 ||
		config.MinimumSamples == 0 || config.MinimumSamples > config.MaxSamplesPerProfile ||
		config.ReservationQuantileBPS == 0 || config.ReservationQuantileBPS > 10000 {
		return fmt.Errorf("%w: profile bounds or sampling policy are invalid", ErrInvalidConfig)
	}
	return nil
}

type profileSample struct {
	value    uint64
	sequence uint64
}

type profileRecord struct {
	profile WorkloadProfile
	key     string
	runs    uint64
	samples []profileSample
	next    uint32
}

type trackedProfileAttempt struct {
	profileKey  string
	decisionSet bool
	sample      bool
}

type ProfileStore struct {
	mu sync.Mutex

	config              ProfileConfig
	profiles            map[string]*profileRecord
	attempts            map[string]*trackedProfileAttempt
	observedSamples     uint64
	failedSamples       uint64
	droppedObservations uint64
	nextSampleSequence  uint64
}

type ProfileStoreSnapshot struct {
	ProfileCount        uint32
	TrackedAttempts     uint32
	ObservedSamples     uint64
	FailedSamples       uint64
	DroppedObservations uint64
}

func NewProfileStore(config ProfileConfig) (*ProfileStore, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &ProfileStore{config: config, profiles: make(map[string]*profileRecord), attempts: make(map[string]*trackedProfileAttempt)}, nil
}

func (store *ProfileStore) EnsureProfile(profile WorkloadProfile) (string, error) {
	if store == nil {
		return "", ErrInvalidProfile
	}
	profileKey, err := profile.Key()
	if err != nil {
		return "", err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureProfileLocked(profileKey, profile); err != nil {
		return "", err
	}
	return profileKey, nil
}

func (store *ProfileStore) ensureProfileLocked(profileKey string, profile WorkloadProfile) error {
	if prior, ok := store.profiles[profileKey]; ok {
		if prior.profile != profile {
			return ErrConflict
		}
		return nil
	}
	if uint32(len(store.profiles)) >= store.config.MaxProfiles {
		return ErrCapacity
	}
	store.profiles[profileKey] = &profileRecord{profile: profile, key: profileKey, samples: make([]profileSample, 0, store.config.MaxSamplesPerProfile)}
	return nil
}

func (store *ProfileStore) profileForKey(profileKey string) (WorkloadProfile, bool) {
	if store == nil {
		return WorkloadProfile{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.profiles[profileKey]
	if !ok {
		return WorkloadProfile{}, false
	}
	return record.profile, true
}

func (store *ProfileStore) RegisterAttempt(attemptID string, profile WorkloadProfile) error {
	if store == nil || !boundedIdentifier(attemptID) {
		return ErrInvalidProfile
	}
	profileKey, err := profile.Key()
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.registerAttemptLocked(attemptID, profile, profileKey)
}

func (store *ProfileStore) registerAttemptLocked(attemptID string, profile WorkloadProfile, profileKey string) error {
	if prior, ok := store.attempts[attemptID]; ok {
		if prior.profileKey != profileKey {
			return ErrConflict
		}
		return nil
	}
	if uint32(len(store.attempts)) >= store.config.MaxTrackedAttempts {
		return ErrCapacity
	}
	if err := store.ensureProfileLocked(profileKey, profile); err != nil {
		return err
	}
	store.attempts[attemptID] = &trackedProfileAttempt{profileKey: profileKey}
	return nil
}

func (store *ProfileStore) ShouldSample(attemptID string) bool {
	if store == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	attempt, ok := store.attempts[attemptID]
	if !ok {
		return false
	}
	if attempt.decisionSet {
		return attempt.sample
	}
	profile := store.profiles[attempt.profileKey]
	if profile == nil {
		delete(store.attempts, attemptID)
		return false
	}
	profile.runs++
	attempt.decisionSet = true
	attempt.sample = profile.runs <= uint64(store.config.ColdRuns) ||
		(profile.runs > uint64(store.config.ColdRuns) && (profile.runs-uint64(store.config.ColdRuns))%uint64(store.config.StableSampleEvery) == 0)
	if !attempt.sample {
		delete(store.attempts, attemptID)
	}
	return attempt.sample
}

func (store *ProfileStore) Observe(observation enginecontract.FootprintObservation) {
	if err := store.RecordObservation(observation); err != nil && store != nil {
		store.mu.Lock()
		store.droppedObservations++
		store.mu.Unlock()
	}
}

func (store *ProfileStore) RecordObservation(observation enginecontract.FootprintObservation) error {
	if store == nil {
		return ErrInvalidProfile
	}
	if err := observation.Validate(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	attempt, ok := store.attempts[observation.AttemptID]
	if !ok || !attempt.decisionSet || !attempt.sample {
		return ErrNotFound
	}
	profile := store.profiles[attempt.profileKey]
	if profile == nil {
		return ErrNotFound
	}
	delete(store.attempts, observation.AttemptID)
	if observation.Status != enginecontract.FootprintObserved {
		store.failedSamples++
		return nil
	}
	store.nextSampleSequence++
	profile.addSample(profileSample{value: observation.Memory.PrivateDirtyBytes, sequence: store.nextSampleSequence}, store.config.MaxSamplesPerProfile)
	store.observedSamples++
	return nil
}

func (profile *profileRecord) addSample(sample profileSample, maximum uint32) {
	if uint32(len(profile.samples)) < maximum {
		profile.samples = append(profile.samples, sample)
		return
	}
	profile.samples[profile.next] = sample
	profile.next = (profile.next + 1) % maximum
}

func (store *ProfileStore) ForgetAttempt(attemptID string) {
	if store == nil {
		return
	}
	store.mu.Lock()
	delete(store.attempts, attemptID)
	store.mu.Unlock()
}

func (store *ProfileStore) CurrentReservationQuantileBPS() uint32 {
	if store == nil {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.config.ReservationQuantileBPS
}

func (store *ProfileStore) compareAndSwapReservationQuantile(previous, next uint32) error {
	if store == nil || next == 0 || next > 10000 {
		return ErrInvalidConfig
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.config.ReservationQuantileBPS != previous {
		return ErrConflict
	}
	store.config.ReservationQuantileBPS = next
	return nil
}

func (store *ProfileStore) Snapshot() ProfileStoreSnapshot {
	if store == nil {
		return ProfileStoreSnapshot{}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return ProfileStoreSnapshot{
		ProfileCount: uint32(len(store.profiles)), TrackedAttempts: uint32(len(store.attempts)),
		ObservedSamples: store.observedSamples, FailedSamples: store.failedSamples, DroppedObservations: store.droppedObservations,
	}
}

var _ enginecontract.FootprintSink = (*ProfileStore)(nil)
