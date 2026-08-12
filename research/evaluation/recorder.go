package evaluation

type recorderPhase uint8

const (
	phasePlanned recorderPhase = iota
	phaseSetup
	phaseStarted
	phaseExecuted
	phaseOracle
	phaseTerminal
)

type RowRecorder struct {
	planned PlannedRow
	row     RawRow
	phase   recorderPhase
}

func NewRowRecorder(planned PlannedRow) (*RowRecorder, error) {
	if planned.RowID != RowIdentity(planned.WorkloadID, planned.Treatment, planned.Repetition) || !identifierPattern.MatchString(planned.WorkloadID) || !validTreatment(planned.Treatment) || planned.Supported && planned.UnsupportedReason != "" || !planned.Supported && planned.UnsupportedReason == "" {
		return nil, ErrInvalid
	}
	recorder := &RowRecorder{planned: planned, row: RawRow{RowID: planned.RowID, WorkloadID: planned.WorkloadID, Treatment: planned.Treatment, Repetition: planned.Repetition, OracleStatus: OracleNotRun}}
	if !planned.Supported {
		recorder.phase = phaseTerminal
		recorder.row.Status = RowUnsupported
		recorder.row.ProblemCode = planned.UnsupportedReason
	}
	return recorder, nil
}

func (r *RowRecorder) RecordSetup(millis uint64) error {
	if r == nil || r.phase != phasePlanned {
		return ErrInvalid
	}
	r.row.PhaseMillis.Setup = millis
	r.phase = phaseSetup
	return nil
}

func (r *RowRecorder) Start() error {
	if r == nil || r.phase != phaseSetup {
		return ErrInvalid
	}
	r.row.Started = true
	r.phase = phaseStarted
	return nil
}

func (r *RowRecorder) RecordExecution(millis uint64) error {
	if r == nil || r.phase != phaseStarted {
		return ErrInvalid
	}
	r.row.PhaseMillis.Execution = millis
	r.phase = phaseExecuted
	return nil
}

func (r *RowRecorder) RecordOracle(status OracleStatus, millis uint64) error {
	if r == nil || r.phase != phaseExecuted || (status != OraclePassed && status != OracleFailed) {
		return ErrInvalid
	}
	r.row.OracleStatus = status
	r.row.PhaseMillis.Oracle = millis
	r.phase = phaseOracle
	return nil
}

func (r *RowRecorder) RecordEvidence(complete bool, millis uint64, metrics RowMetrics, problemCode string) error {
	if r == nil || r.phase != phaseOracle || metrics.ReusedObjectCount > metrics.ObjectCount || (complete && problemCode != "") || (!complete && problemCode == "") {
		return ErrInvalid
	}
	r.row.PhaseMillis.Evidence = millis
	r.row.Metrics = metrics
	r.row.EvidenceComplete = complete
	r.phase = phaseTerminal
	if !complete {
		r.row.Status = RowFailed
		r.row.ProblemCode = problemCode
		return nil
	}
	if r.row.OracleStatus == OraclePassed {
		r.row.Status = RowCompleted
	} else {
		r.row.Status = RowFailed
		r.row.ProblemCode = "oracle_mismatch"
	}
	return nil
}

func (r *RowRecorder) Timeout(millis uint64, problemCode string) error {
	if r == nil || r.phase != phaseStarted || problemCode == "" {
		return ErrInvalid
	}
	r.row.PhaseMillis.Execution = millis
	r.row.Status = RowTimedOut
	r.row.OracleStatus = OracleNotRun
	r.row.ProblemCode = problemCode
	r.phase = phaseTerminal
	return nil
}

func (r *RowRecorder) Finalize() (RawRow, error) {
	if r == nil || r.phase != phaseTerminal || validateRawRow(r.row) != nil {
		return RawRow{}, ErrInvalid
	}
	return r.row, nil
}
