package evaluationv2

import (
	"encoding/json"
	"sort"
)

type catalogDocument struct {
	Items []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Score uint32 `json:"score"`
	} `json:"items"`
}

type manifestDocument struct {
	Suite struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"suite"`
	Cases []struct {
		ID             string `json:"id"`
		TaskClass      string `json:"task_class"`
		InputArtifacts []struct {
			ID string `json:"id"`
		} `json:"input_artifacts"`
		Metrics []struct {
			ID        string `json:"id"`
			Direction string `json:"direction"`
			Unit      string `json:"unit"`
			Bounds    struct {
				Minimum uint32 `json:"minimum"`
				Maximum uint32 `json:"maximum"`
			} `json:"bounds"`
		} `json:"metrics"`
	} `json:"cases"`
}

func decodeCatalog(raw json.RawMessage) (catalogDocument, error) {
	var value catalogDocument
	if err := json.Unmarshal(raw, &value); err != nil || len(value.Items) == 0 {
		return catalogDocument{}, ErrInvalid
	}
	return value, nil
}

func decodeManifest(raw json.RawMessage) (manifestDocument, error) {
	var value manifestDocument
	if err := json.Unmarshal(raw, &value); err != nil || len(value.Cases) == 0 {
		return manifestDocument{}, ErrInvalid
	}
	return value, nil
}

func bestCatalogItem(catalog catalogDocument) int {
	best := 0
	for i := 1; i < len(catalog.Items); i++ {
		if catalog.Items[i].Score > catalog.Items[best].Score || catalog.Items[i].Score == catalog.Items[best].Score && catalog.Items[i].ID < catalog.Items[best].ID {
			best = i
		}
	}
	return best
}

func transformExpandedDirect(id string, results map[string]json.RawMessage, inputs json.RawMessage) (json.RawMessage, error) {
	switch id {
	case "catalog-top-direct", "catalog-threshold-loop":
		catalog, err := decodeCatalog(results["sources.demo_catalog"])
		if err != nil {
			return nil, err
		}
		if id == "catalog-top-direct" {
			best := catalog.Items[bestCatalogItem(catalog)]
			return json.Marshal(struct {
				ID    string `json:"id"`
				Score uint32 `json:"score"`
				Title string `json:"title"`
			}{best.ID, best.Score, best.Title})
		}
		var parameters struct {
			MinimumScore uint32 `json:"minimum_score"`
		}
		if err := json.Unmarshal(inputs, &parameters); err != nil {
			return nil, ErrInvalid
		}
		items := append([]struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Score uint32 `json:"score"`
		}(nil), catalog.Items...)
		sort.Slice(items, func(i, j int) bool {
			if items[i].Score != items[j].Score {
				return items[i].Score > items[j].Score
			}
			return items[i].ID < items[j].ID
		})
		ids := []string{}
		var total uint32
		for _, item := range items {
			if item.Score >= parameters.MinimumScore {
				ids = append(ids, item.ID)
				total += item.Score
			}
		}
		return json.Marshal(struct {
			Count        int      `json:"count"`
			IDs          []string `json:"ids"`
			MinimumScore uint32   `json:"minimum_score"`
			ScoreTotal   uint32   `json:"score_total"`
		}{len(ids), ids, parameters.MinimumScore, total})
	case "manifest-suite-direct", "manifest-matrix":
		manifest, err := decodeManifest(results["sources.benchmark_manifest"])
		if err != nil {
			return nil, err
		}
		suite := manifest.Suite.ID + "@" + manifest.Suite.Version
		if id == "manifest-suite-direct" {
			artifacts, metrics := 0, 0
			for _, item := range manifest.Cases {
				artifacts += len(item.InputArtifacts)
				metrics += len(item.Metrics)
			}
			return json.Marshal(struct {
				ArtifactCount int    `json:"artifact_count"`
				CaseCount     int    `json:"case_count"`
				MetricCount   int    `json:"metric_count"`
				Suite         string `json:"suite"`
			}{artifacts, len(manifest.Cases), metrics, suite})
		}
		type matrixRow struct {
			ArtifactIDs []string `json:"artifact_ids"`
			CaseID      string   `json:"case_id"`
			Direction   string   `json:"direction"`
			Maximum     uint32   `json:"maximum"`
			MetricID    string   `json:"metric_id"`
			Minimum     uint32   `json:"minimum"`
			TaskClass   string   `json:"task_class"`
			Unit        string   `json:"unit"`
		}
		rows := []matrixRow{}
		for _, item := range manifest.Cases {
			artifacts := make([]string, len(item.InputArtifacts))
			for i := range item.InputArtifacts {
				artifacts[i] = item.InputArtifacts[i].ID
			}
			sort.Strings(artifacts)
			for _, metric := range item.Metrics {
				rows = append(rows, matrixRow{artifacts, item.ID, metric.Direction, metric.Bounds.Maximum, metric.ID, metric.Bounds.Minimum, item.TaskClass, metric.Unit})
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].CaseID != rows[j].CaseID {
				return rows[i].CaseID < rows[j].CaseID
			}
			return rows[i].MetricID < rows[j].MetricID
		})
		return json.Marshal(struct {
			Rows  []matrixRow `json:"rows"`
			Suite string      `json:"suite"`
		}{rows, suite})
	case "source-join-ranking":
		catalog, err := decodeCatalog(results["sources.demo_catalog"])
		if err != nil {
			return nil, err
		}
		manifest, err := decodeManifest(results["sources.benchmark_manifest"])
		if err != nil {
			return nil, err
		}
		best := catalog.Items[bestCatalogItem(catalog)]
		selected := manifest.Cases[0]
		for _, item := range manifest.Cases[1:] {
			if item.ID < selected.ID {
				selected = item
			}
		}
		metrics := make([]string, len(selected.Metrics))
		for i := range selected.Metrics {
			metrics[i] = selected.Metrics[i].ID
		}
		sort.Strings(metrics)
		return json.Marshal(struct {
			CatalogID    string   `json:"catalog_id"`
			CatalogScore uint32   `json:"catalog_score"`
			CaseID       string   `json:"case_id"`
			MetricIDs    []string `json:"metric_ids"`
			Suite        string   `json:"suite"`
			TaskClass    string   `json:"task_class"`
		}{best.ID, best.Score, selected.ID, metrics, manifest.Suite.ID + "@" + manifest.Suite.Version, selected.TaskClass})
	default:
		return nil, ErrInvalid
	}
}
