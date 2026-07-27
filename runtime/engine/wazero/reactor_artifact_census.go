package wazero

import (
	"errors"
	"fmt"
	"sort"

	wabinbinary "github.com/tetratelabs/wabin/binary"
	wabinwasm "github.com/tetratelabs/wabin/wasm"
)

type ReactorImportCensus struct {
	FunctionCount int `json:"function_count"`
	TableCount    int `json:"table_count"`
	MemoryCount   int `json:"memory_count"`
	GlobalCount   int `json:"global_count"`
}

type ReactorGlobalCensus struct {
	Count                  int      `json:"count"`
	ImportedCount          int      `json:"imported_count"`
	DefinedCount           int      `json:"defined_count"`
	MutableCount           int      `json:"mutable_count"`
	ImmutableCount         int      `json:"immutable_count"`
	ExportedMutableNames   []string `json:"exported_mutable_names"`
	UnexportedMutableCount int      `json:"unexported_mutable_count"`
}

type ReactorTableCensus struct {
	Count         int `json:"count"`
	ImportedCount int `json:"imported_count"`
	DefinedCount  int `json:"defined_count"`
}

type ReactorElementCensus struct {
	Count            int `json:"count"`
	ActiveCount      int `json:"active_count"`
	PassiveCount     int `json:"passive_count"`
	DeclarativeCount int `json:"declarative_count"`
}

type ReactorDataCensus struct {
	Count        int `json:"count"`
	ActiveCount  int `json:"active_count"`
	PassiveCount int `json:"passive_count"`
}

// ReactorArtifactCensus is a complete static inventory of non-memory Wasm
// definitions that wazero's public CompiledModule API doesn't expose. It proves
// that a state class exists; it does not prove that the class is resettable.
type ReactorArtifactCensus struct {
	SchemaVersion    int                  `json:"schema_version"`
	ParseComplete    bool                 `json:"parse_complete"`
	ImportCount      int                  `json:"import_count"`
	ImportModules    []string             `json:"import_modules"`
	Imports          ReactorImportCensus  `json:"imports"`
	Globals          ReactorGlobalCensus  `json:"globals"`
	Tables           ReactorTableCensus   `json:"tables"`
	Elements         ReactorElementCensus `json:"elements"`
	Data             ReactorDataCensus    `json:"data"`
	HasStartFunction bool                 `json:"has_start_function"`
}

func (census ReactorArtifactCensus) Validate() error {
	if census.SchemaVersion != 1 || !census.ParseComplete {
		return errors.New("reactor artifact census is incomplete")
	}
	if census.ImportCount != census.Imports.FunctionCount+census.Imports.TableCount+census.Imports.MemoryCount+census.Imports.GlobalCount {
		return errors.New("reactor artifact import census is inconsistent")
	}
	if census.Globals.Count != census.Globals.ImportedCount+census.Globals.DefinedCount ||
		census.Globals.Count != census.Globals.MutableCount+census.Globals.ImmutableCount ||
		census.Globals.ImportedCount != census.Imports.GlobalCount {
		return errors.New("reactor artifact global census is inconsistent")
	}
	if census.Globals.MutableCount != len(census.Globals.ExportedMutableNames)+census.Globals.UnexportedMutableCount ||
		!sort.StringsAreSorted(census.Globals.ExportedMutableNames) {
		return errors.New("reactor artifact mutable-global visibility is inconsistent")
	}
	for index, name := range census.Globals.ExportedMutableNames {
		if name == "" || index > 0 && name == census.Globals.ExportedMutableNames[index-1] {
			return errors.New("reactor artifact mutable-global exports are incomplete")
		}
	}
	if census.Tables.Count != census.Tables.ImportedCount+census.Tables.DefinedCount || census.Tables.ImportedCount != census.Imports.TableCount {
		return errors.New("reactor artifact table census is inconsistent")
	}
	if census.Elements.Count != census.Elements.ActiveCount+census.Elements.PassiveCount+census.Elements.DeclarativeCount {
		return errors.New("reactor artifact element census is inconsistent")
	}
	if census.Data.Count != census.Data.ActiveCount+census.Data.PassiveCount {
		return errors.New("reactor artifact data census is inconsistent")
	}
	if !sort.StringsAreSorted(census.ImportModules) {
		return errors.New("reactor artifact import modules are not canonical")
	}
	for index, module := range census.ImportModules {
		if module == "" || index > 0 && module == census.ImportModules[index-1] {
			return errors.New("reactor artifact import modules are incomplete")
		}
	}
	return nil
}

func censusReactorArtifact(wasm []byte) (ReactorArtifactCensus, error) {
	census := ReactorArtifactCensus{SchemaVersion: 1, ImportModules: []string{}, Globals: ReactorGlobalCensus{ExportedMutableNames: []string{}}}
	module, err := wabinbinary.DecodeModule(wasm, wabinwasm.CoreFeaturesV2)
	if err != nil {
		return census, fmt.Errorf("decode reactor artifact state: %w", err)
	}
	modules := map[string]struct{}{}
	globalMutable := make([]bool, 0)
	for _, imported := range module.ImportSection {
		if imported == nil {
			return census, errors.New("reactor artifact contains a nil import")
		}
		census.ImportCount++
		modules[imported.Module] = struct{}{}
		switch imported.Type {
		case wabinwasm.ExternTypeFunc:
			census.Imports.FunctionCount++
		case wabinwasm.ExternTypeTable:
			if imported.DescTable == nil {
				return census, errors.New("reactor artifact imported table is incomplete")
			}
			census.Imports.TableCount++
			census.Tables.ImportedCount++
		case wabinwasm.ExternTypeMemory:
			if imported.DescMem == nil {
				return census, errors.New("reactor artifact imported memory is incomplete")
			}
			census.Imports.MemoryCount++
		case wabinwasm.ExternTypeGlobal:
			if imported.DescGlobal == nil {
				return census, errors.New("reactor artifact imported global is incomplete")
			}
			census.Imports.GlobalCount++
			census.Globals.ImportedCount++
			if imported.DescGlobal.Mutable {
				census.Globals.MutableCount++
			} else {
				census.Globals.ImmutableCount++
			}
			globalMutable = append(globalMutable, imported.DescGlobal.Mutable)
		default:
			return census, fmt.Errorf("reactor artifact has unsupported import type %#x", imported.Type)
		}
	}
	for module := range modules {
		census.ImportModules = append(census.ImportModules, module)
	}
	sort.Strings(census.ImportModules)

	for _, global := range module.GlobalSection {
		if global == nil || global.Type == nil {
			return census, errors.New("reactor artifact defined global is incomplete")
		}
		census.Globals.DefinedCount++
		if global.Type.Mutable {
			census.Globals.MutableCount++
		} else {
			census.Globals.ImmutableCount++
		}
		globalMutable = append(globalMutable, global.Type.Mutable)
	}
	census.Globals.Count = census.Globals.ImportedCount + census.Globals.DefinedCount
	for _, exported := range module.ExportSection {
		if exported == nil || exported.Type != wabinwasm.ExternTypeGlobal {
			continue
		}
		if int(exported.Index) >= len(globalMutable) {
			return census, errors.New("reactor artifact global export index is invalid")
		}
		if globalMutable[exported.Index] {
			census.Globals.ExportedMutableNames = append(census.Globals.ExportedMutableNames, exported.Name)
		}
	}
	sort.Strings(census.Globals.ExportedMutableNames)
	census.Globals.UnexportedMutableCount = census.Globals.MutableCount - len(census.Globals.ExportedMutableNames)

	census.Tables.DefinedCount = len(module.TableSection)
	census.Tables.Count = census.Tables.ImportedCount + census.Tables.DefinedCount

	for _, element := range module.ElementSection {
		if element == nil {
			return census, errors.New("reactor artifact contains a nil element segment")
		}
		census.Elements.Count++
		switch element.Mode {
		case wabinwasm.ElementModeActive:
			census.Elements.ActiveCount++
		case wabinwasm.ElementModePassive:
			census.Elements.PassiveCount++
		case wabinwasm.ElementModeDeclarative:
			census.Elements.DeclarativeCount++
		default:
			return census, fmt.Errorf("reactor artifact has unsupported element mode %#x", element.Mode)
		}
	}

	for _, data := range module.DataSection {
		if data == nil {
			return census, errors.New("reactor artifact contains a nil data segment")
		}
		census.Data.Count++
		if data.OffsetExpression == nil {
			census.Data.PassiveCount++
		} else {
			census.Data.ActiveCount++
		}
	}
	census.HasStartFunction = module.StartSection != nil
	census.ParseComplete = true
	if err := census.Validate(); err != nil {
		return ReactorArtifactCensus{SchemaVersion: 1}, err
	}
	return census, nil
}
