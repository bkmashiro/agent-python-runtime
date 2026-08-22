package semantic

import (
	"context"
	"errors"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

// VerifiedSourceGenerationConfig admits only complete statement prefixes that
// have actually arrived. It never receives a future or hidden full source.
type VerifiedSourceGenerationConfig struct {
	Analyzer *wazeroengine.Engine
	Analyze  func(context.Context, string, Bindings, *capability.Plan) (VerifiedAnalysis, error)
	// ShouldAnalyzePrefix may conservatively skip prefixes that cannot add candidates.
	// A false result only reduces speculation; the final source is still sealed exactly.
	ShouldAnalyzePrefix func(prefixIndex uint32, source string) bool
	Plan                *capability.Plan
	Bindings            Bindings
	Admission           *StreamingPrefixAdmission
	SourceChunks        <-chan string
	Observe             func(VerifiedSourceGenerationEvent)
}

type VerifiedSourceGenerationEvent struct {
	Phase        string
	PrefixIndex  uint32
	SourceBytes  uint32
	AddedCalls   uint32
	ElapsedNanos uint64
	Complete     bool
	SourceSHA256 string
}

type GeneratedSource struct {
	source     string
	sha256     string
	controller *StreamingSemanticPreDispatch
	binding    *generatedSourceBinding
}

type generatedSourceBinding struct {
	mu       sync.Mutex
	executed bool
}

func (generated GeneratedSource) Source() string { return generated.source }
func (generated GeneratedSource) SHA256() string { return generated.sha256 }

func GenerateVerifiedSourceWithPreDispatch(ctx context.Context, config VerifiedSourceGenerationConfig) (GeneratedSource, error) {
	if ctx == nil || (config.Analyzer == nil && config.Analyze == nil) || config.Plan == nil || config.Admission == nil ||
		config.Admission.plan != config.Plan || config.SourceChunks == nil {
		return GeneratedSource{}, ErrPreDispatchInvalid
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = config.Admission.controller.Finalize(false)
		}
	}()
	type analysisResult struct {
		index       uint32
		prefixIndex uint32
		source      string
		verified    VerifiedAnalysis
		err         error
	}
	results := make(chan analysisResult, 32)
	var analyzerMu sync.Mutex
	analyze := config.Analyze
	if analyze == nil {
		analyze = func(callContext context.Context, source string, bindings Bindings, plan *capability.Plan) (VerifiedAnalysis, error) {
			analyzerMu.Lock()
			defer analyzerMu.Unlock()
			request, err := NewRequest(source, bindings, plan)
			if err != nil {
				return VerifiedAnalysis{}, err
			}
			return AnalyzeVerified(callContext, config.Analyzer, request)
		}
	}
	var source string
	var visible uint32
	var scheduled uint32
	var nextCommit uint32 = 1
	pending := 0
	sourceChannel := config.SourceChunks
	completed := false
	committed := make(map[uint32]analysisResult)
	var finalVerified VerifiedAnalysis
	var finalVerifiedSource string
	for sourceChannel != nil || pending > 0 {
		select {
		case <-runContext.Done():
			return GeneratedSource{}, runContext.Err()
		case chunk, ok := <-sourceChannel:
			if !ok {
				if source == "" {
					return GeneratedSource{}, errors.New("verified source generation produced no source")
				}
				sourceChannel = nil
				completed = true
				if config.Observe != nil {
					config.Observe(VerifiedSourceGenerationEvent{
						Phase: "source_complete", PrefixIndex: visible, SourceBytes: uint32(len(source)), Complete: true,
						SourceSHA256: digestText(source),
					})
				}
				continue
			}
			if chunk == "" {
				return GeneratedSource{}, errors.New("verified source chunk is empty")
			}
			if len(source) > streaming.MaxSourceBytes || len(chunk) > streaming.MaxSourceBytes-len(source) {
				return GeneratedSource{}, streaming.ErrSourceTooLarge
			}
			source += chunk
			visible++
			prefixIndex := visible
			prefixSource := source
			if config.Observe != nil {
				config.Observe(VerifiedSourceGenerationEvent{
					Phase: "prefix_visible", PrefixIndex: prefixIndex, SourceBytes: uint32(len(prefixSource)),
				})
			}
			if config.ShouldAnalyzePrefix != nil && !config.ShouldAnalyzePrefix(prefixIndex, prefixSource) {
				if err := config.Admission.RecordSkippedPrefix(prefixSource); err != nil {
					return GeneratedSource{}, err
				}
				if config.Observe != nil {
					config.Observe(VerifiedSourceGenerationEvent{
						Phase: "prefix_skipped", PrefixIndex: prefixIndex, SourceBytes: uint32(len(prefixSource)),
					})
				}
				continue
			}
			scheduled++
			scheduleIndex := scheduled
			pending++
			go func() {
				verified, err := analyze(runContext, prefixSource, config.Bindings, config.Plan)
				select {
				case results <- analysisResult{index: scheduleIndex, prefixIndex: prefixIndex, source: prefixSource, verified: verified, err: err}:
				case <-runContext.Done():
				}
			}()
		case result := <-results:
			pending--
			if result.err != nil {
				return GeneratedSource{}, result.err
			}
			committed[result.index] = result
			for {
				ready, ok := committed[nextCommit]
				if !ok {
					break
				}
				admissionStarted := time.Now()
				added, err := config.Admission.AdmitVerifiedPrefix(ctx, ready.source, ready.verified)
				admissionNanos := uint64(time.Since(admissionStarted))
				if err != nil {
					return GeneratedSource{}, err
				}
				finalVerified = ready.verified
				finalVerifiedSource = ready.source
				delete(committed, nextCommit)
				if config.Observe != nil {
					config.Observe(VerifiedSourceGenerationEvent{
						Phase: "prefix_admitted", PrefixIndex: ready.prefixIndex, SourceBytes: uint32(len(ready.source)), AddedCalls: added, ElapsedNanos: admissionNanos,
						SourceSHA256: config.Admission.Snapshot().LastSourceSHA256,
					})
				}
				nextCommit++
			}
		}
	}
	if !completed || nextCommit != scheduled+1 {
		return GeneratedSource{}, ErrPreDispatchInvalid
	}
	if finalVerifiedSource != source {
		verified, err := analyze(runContext, source, config.Bindings, config.Plan)
		if err != nil {
			return GeneratedSource{}, err
		}
		finalVerified = verified
	}
	if err := config.Admission.SealFinalSource(source, finalVerified); err != nil {
		return GeneratedSource{}, err
	}
	snapshot := config.Admission.Snapshot()
	if config.Observe != nil {
		config.Observe(VerifiedSourceGenerationEvent{
			Phase: "source_sealed", PrefixIndex: visible, SourceBytes: uint32(len(source)), Complete: true,
			SourceSHA256: snapshot.LastSourceSHA256,
		})
	}
	succeeded = true
	return GeneratedSource{
		source: source, sha256: snapshot.LastSourceSHA256,
		controller: config.Admission.controller, binding: &generatedSourceBinding{},
	}, nil
}

// ExecuteGeneratedSource starts the fresh execution Guest only after final
// source sealing, and verifies that the normal RunRequest contains exactly that
// complete source. The controller remains one-Run/one-execution.
func ExecuteGeneratedSource(
	ctx context.Context,
	runner enginecontract.Runner,
	attempt *workspace.Attempt,
	request []byte,
	trustedPrepare string,
	generated GeneratedSource,
) (streaming.RunResult, error) {
	return ExecuteGeneratedSourceObserved(ctx, runner, attempt, request, trustedPrepare, generated, nil)
}

// GeneratedExecutionOutcome preserves the exact generated-source binding while
// exposing a body-bearing response only to the immediate Host consumer. The
// response is never evidence: callers must project a canonical result digest or
// error class before persistence. Successful responses publish the private
// workspace attempt; Python error responses discard it.
type GeneratedExecutionOutcome struct {
	Response             []byte
	WorkspaceDisposition string
}

// ExecuteGeneratedSourceOutcome is the non-success-aware companion to
// ExecuteGeneratedSource. It keeps the same one-shot source/controller binding
// but returns a validated Python error envelope after discarding the attempt.
func ExecuteGeneratedSourceOutcome(
	ctx context.Context,
	runner enginecontract.Runner,
	attempt *workspace.Attempt,
	request []byte,
	trustedPrepare string,
	generated GeneratedSource,
) (GeneratedExecutionOutcome, error) {
	if ctx == nil || runner == nil || attempt == nil || generated.binding == nil || generated.controller == nil ||
		generated.source == "" || generated.sha256 == "" {
		return GeneratedExecutionOutcome{}, ErrPreDispatchInvalid
	}
	decoded, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil || decoded.Code != generated.source {
		return GeneratedExecutionOutcome{}, ErrAnalysisBinding
	}
	snapshot := generated.controller.Snapshot()
	if !snapshot.SourceSealed || snapshot.FinalSourceSHA256 != generated.sha256 {
		return GeneratedExecutionOutcome{}, ErrAnalysisBinding
	}
	generated.binding.mu.Lock()
	if generated.binding.executed {
		generated.binding.mu.Unlock()
		return GeneratedExecutionOutcome{}, ErrPreDispatchInvalid
	}
	generated.binding.executed = true
	generated.binding.mu.Unlock()
	response, runErr := runner.Run(ctx, request, trustedPrepare)
	if runErr != nil {
		_ = runner.Close(ctx)
		_ = attempt.Discard()
		return GeneratedExecutionOutcome{}, runErr
	}
	if err := runner.Close(ctx); err != nil {
		_ = attempt.Discard()
		return GeneratedExecutionOutcome{}, err
	}
	validated, err := runtimeconfig.DecodeAndValidateRunResponse(decoded, response)
	if err != nil {
		_ = attempt.Discard()
		return GeneratedExecutionOutcome{}, err
	}
	switch validated.Status {
	case runtimeconfig.RunResponseOK:
		if _, err := attempt.Publish(); err != nil {
			_ = attempt.Discard()
			return GeneratedExecutionOutcome{}, err
		}
		return GeneratedExecutionOutcome{Response: append([]byte(nil), response...), WorkspaceDisposition: "published"}, nil
	case runtimeconfig.RunResponseError:
		if err := attempt.Discard(); err != nil {
			return GeneratedExecutionOutcome{}, err
		}
		return GeneratedExecutionOutcome{Response: append([]byte(nil), response...), WorkspaceDisposition: "discarded"}, nil
	default:
		_ = attempt.Discard()
		return GeneratedExecutionOutcome{}, ErrPreDispatchInvalid
	}
}

func ExecuteGeneratedSourceObserved(
	ctx context.Context,
	runner enginecontract.Runner,
	attempt *workspace.Attempt,
	request []byte,
	trustedPrepare string,
	generated GeneratedSource,
	observe func(enginecontract.Runner) error,
) (streaming.RunResult, error) {
	if ctx == nil || runner == nil || attempt == nil || generated.binding == nil || generated.controller == nil ||
		generated.source == "" || generated.sha256 == "" {
		return streaming.RunResult{}, ErrPreDispatchInvalid
	}
	decoded, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil || decoded.Code != generated.source {
		return streaming.RunResult{}, ErrAnalysisBinding
	}
	snapshot := generated.controller.Snapshot()
	if !snapshot.SourceSealed || snapshot.FinalSourceSHA256 != generated.sha256 {
		return streaming.RunResult{}, ErrAnalysisBinding
	}
	generated.binding.mu.Lock()
	if generated.binding.executed {
		generated.binding.mu.Unlock()
		return streaming.RunResult{}, ErrPreDispatchInvalid
	}
	generated.binding.executed = true
	generated.binding.mu.Unlock()
	return streaming.ExecuteObserved(ctx, runner, attempt, request, trustedPrepare, observe)
}
