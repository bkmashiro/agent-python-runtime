package semanticspeculation

import (
	"errors"
	"sync"
	"time"
)

var ErrPhase5StageActive = errors.New("phase 5 stage still active")
var ErrPhase5ClockRegression = errors.New("phase 5 monotonic clock regressed")

var errPhase5RecorderClosed = errors.New("phase 5 stage recorder closed")
var errPhase5StageState = errors.New("invalid phase 5 stage lifecycle")

type Phase5StageToken struct {
	name       string
	generation uint64
}

type Phase5TrialTimeline struct {
	CriticalPathStartedOffsetNanos uint64                   `json:"critical_path_started_offset_nanos"`
	TrialEndedOffsetNanos          uint64                   `json:"trial_ended_offset_nanos"`
	TotalCriticalPathNanos         uint64                   `json:"total_critical_path_nanos"`
	UnattributedCriticalPathNanos  uint64                   `json:"unattributed_critical_path_nanos"`
	Stages                         []Phase5StageObservation `json:"stages"`
}

type phase5RecordedStage struct {
	disposition string
	started     uint64
	ended       uint64
	generation  uint64
	active      bool
	complete    bool
}

type Phase5StageRecorder struct {
	mu            sync.Mutex
	now           func() time.Time
	origin        time.Time
	lastOffset    uint64
	criticalStart uint64
	criticalSet   bool
	closed        bool
	nextToken     uint64
	stages        map[string]phase5RecordedStage
}

func NewPhase5StageRecorder(now func() time.Time) (*Phase5StageRecorder, error) {
	if now == nil {
		return nil, errPhase5StageState
	}
	origin := now()
	if origin.IsZero() {
		return nil, errPhase5StageState
	}
	return &Phase5StageRecorder{now: now, origin: origin, stages: map[string]phase5RecordedStage{}}, nil
}

func (recorder *Phase5StageRecorder) MarkCriticalPathStart() error {
	if recorder == nil {
		return errPhase5StageState
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.closed || recorder.criticalSet {
		return errPhase5StageState
	}
	offset, err := recorder.observeLocked()
	if err != nil {
		return err
	}
	for _, stage := range recorder.stages {
		if stage.active {
			return ErrPhase5StageActive
		}
	}
	recorder.criticalStart = offset
	recorder.criticalSet = true
	return nil
}

func (recorder *Phase5StageRecorder) Start(name, disposition string) (Phase5StageToken, error) {
	if recorder == nil || !phase5TimingStageExists(name) || (disposition != Phase5StageMeasured && disposition != Phase5StagePreclock) {
		return Phase5StageToken{}, errPhase5StageState
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.closed {
		return Phase5StageToken{}, errPhase5RecorderClosed
	}
	if existing, exists := recorder.stages[name]; exists && (existing.active || existing.complete) {
		return Phase5StageToken{}, errPhase5StageState
	}
	if disposition == Phase5StageMeasured && !recorder.criticalSet {
		return Phase5StageToken{}, errPhase5StageState
	}
	if disposition == Phase5StagePreclock && recorder.criticalSet {
		return Phase5StageToken{}, errPhase5StageState
	}
	offset, err := recorder.observeLocked()
	if err != nil {
		return Phase5StageToken{}, err
	}
	recorder.nextToken++
	stage := phase5RecordedStage{disposition: disposition, started: offset, generation: recorder.nextToken, active: true}
	recorder.stages[name] = stage
	return Phase5StageToken{name: name, generation: stage.generation}, nil
}

func (recorder *Phase5StageRecorder) End(token Phase5StageToken) error {
	if recorder == nil || token.name == "" || token.generation == 0 {
		return errPhase5StageState
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.closed {
		return errPhase5RecorderClosed
	}
	stage, exists := recorder.stages[token.name]
	if !exists || !stage.active || stage.complete || stage.generation != token.generation {
		return errPhase5StageState
	}
	offset, err := recorder.observeLocked()
	if err != nil {
		return err
	}
	if offset < stage.started {
		return ErrPhase5ClockRegression
	}
	stage.ended = offset
	stage.active = false
	stage.complete = true
	recorder.stages[token.name] = stage
	return nil
}

func (recorder *Phase5StageRecorder) Finalize() (Phase5TrialTimeline, error) {
	if recorder == nil {
		return Phase5TrialTimeline{}, errPhase5StageState
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.closed {
		return Phase5TrialTimeline{}, errPhase5RecorderClosed
	}
	if !recorder.criticalSet {
		return Phase5TrialTimeline{}, errPhase5StageState
	}
	for _, stage := range recorder.stages {
		if stage.active {
			return Phase5TrialTimeline{}, ErrPhase5StageActive
		}
	}
	ended, err := recorder.observeLocked()
	if err != nil {
		return Phase5TrialTimeline{}, err
	}
	stages := make([]Phase5StageObservation, 0, len(phase5TimingStages))
	for _, name := range phase5TimingStages {
		stage, exists := recorder.stages[name]
		if !exists {
			stages = append(stages, Phase5StageObservation{Name: name, Disposition: Phase5StageNotApplicable})
			continue
		}
		if !stage.complete || stage.ended < stage.started {
			return Phase5TrialTimeline{}, errPhase5StageState
		}
		stages = append(stages, Phase5StageObservation{Name: name, Disposition: stage.disposition, StartedOffsetNanos: stage.started, EndedOffsetNanos: stage.ended, DurationNanos: stage.ended - stage.started, OnCriticalPath: stage.disposition == Phase5StageMeasured})
	}
	total := ended - recorder.criticalStart
	union := phase5CriticalStageUnion(stages, recorder.criticalStart, ended)
	if union > total {
		return Phase5TrialTimeline{}, errPhase5StageState
	}
	recorder.closed = true
	return Phase5TrialTimeline{CriticalPathStartedOffsetNanos: recorder.criticalStart, TrialEndedOffsetNanos: ended, TotalCriticalPathNanos: total, UnattributedCriticalPathNanos: total - union, Stages: stages}, nil
}

func (recorder *Phase5StageRecorder) observeLocked() (uint64, error) {
	current := recorder.now()
	if current.Before(recorder.origin) {
		return 0, ErrPhase5ClockRegression
	}
	delta := current.Sub(recorder.origin)
	if delta < 0 {
		return 0, ErrPhase5ClockRegression
	}
	offset := uint64(delta)
	if offset < recorder.lastOffset {
		return 0, ErrPhase5ClockRegression
	}
	recorder.lastOffset = offset
	return offset, nil
}

func phase5TimingStageExists(candidate string) bool {
	for _, name := range phase5TimingStages {
		if name == candidate {
			return true
		}
	}
	return false
}
