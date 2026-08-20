package semanticspeculation

const (
	EagerComparatorSchemaVersion = "pysolate.eager-style-gate-contract.v1"
	EagerStyleGateV1Identity     = "sha256:16e76c741749bddb61c68ae80902827f73e9dc7efdad6208d858f8edae100ab8"
)

type EagerComparatorSource struct {
	Title     string `json:"title"`
	ArxivID   string `json:"arxiv_id"`
	PDFURL    string `json:"pdf_url"`
	PDFSHA256 string `json:"pdf_sha256"`
}

type EagerChunkingContract struct {
	Parser               string `json:"parser"`
	Boundary             string `json:"boundary"`
	LookaheadTokens      uint32 `json:"lookahead_tokens"`
	FinalSuffixMustParse bool   `json:"final_suffix_must_parse"`
}

type EagerExecutionContract struct {
	PersistentInterpreter bool   `json:"persistent_interpreter"`
	DynamicBatching       bool   `json:"dynamic_batching"`
	EarlyInterrupt        bool   `json:"early_interrupt"`
	ErrorReport           string `json:"error_report"`
}

type EagerGateContract struct {
	LowYieldNodes         []string `json:"low_yield_nodes"`
	DeniedModules         []string `json:"denied_modules"`
	DynamicNames          []string `json:"dynamic_names"`
	NameMatch             string   `json:"name_match"`
	DeniedAction          string   `json:"denied_action"`
	InvalidFinalSuffix    string   `json:"invalid_final_suffix"`
	PreviouslyRunPrefixes string   `json:"previously_run_prefixes"`
}

type EagerComparatorContract struct {
	SchemaVersion string                 `json:"schema_version"`
	Treatment     string                 `json:"treatment"`
	TargetPython  string                 `json:"target_python"`
	Source        EagerComparatorSource  `json:"source"`
	Chunking      EagerChunkingContract  `json:"chunking"`
	Execution     EagerExecutionContract `json:"execution"`
	Gate          EagerGateContract      `json:"gate"`
	Identity      string                 `json:"identity"`
}

func NewEagerStyleGateV1(targetPython string) (EagerComparatorContract, error) {
	value := EagerComparatorContract{
		SchemaVersion: EagerComparatorSchemaVersion,
		Treatment:     "eager_style_gate",
		TargetPython:  targetPython,
		Source: EagerComparatorSource{
			Title:   "Executing as You Generate: Hiding Execution Latency in LLM Code Generation",
			ArxivID: "2604.00491", PDFURL: "https://arxiv.org/pdf/2604.00491",
			PDFSHA256: "sha256:23af671ca94b7cbbc0866a37391520ae39e75c964320e7809b1612dfb3e023cb",
		},
		Chunking: EagerChunkingContract{
			Parser: "target_cpython_ast", Boundary: "complete_top_level_statement",
			LookaheadTokens: 1, FinalSuffixMustParse: true,
		},
		Execution: EagerExecutionContract{
			PersistentInterpreter: true, DynamicBatching: true, EarlyInterrupt: false,
			ErrorReport: "after_frozen_source_schedule",
		},
		Gate: EagerGateContract{
			LowYieldNodes: []string{"AsyncFunctionDef", "ClassDef", "FunctionDef"},
			DeniedModules: []string{"multiprocessing", "os", "shutil", "signal", "socket", "subprocess", "threading", "time"},
			DynamicNames:  []string{"__import__", "compile", "eval", "exec"},
			NameMatch:     "ast_name_or_attribute_root", DeniedAction: "seal_remaining_suffix",
			InvalidFinalSuffix: "syntax_error_after_source_seal", PreviouslyRunPrefixes: "not_replayed",
		},
	}
	if !validEagerComparatorContract(value, false) {
		return EagerComparatorContract{}, ErrInvalidPreregistration
	}
	return value, nil
}
