package labview

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"strings"
)

const paperFigureWidth = 1200

func figureDemo(snapshot LatestSnapshot, id string) (LatestDemo, error) {
	for _, demo := range snapshot.Demos {
		if demo.ID == id {
			return demo, nil
		}
	}
	return LatestDemo{}, fmt.Errorf("paper figure demo %q missing", id)
}

func figureTone(tone string) string {
	switch tone {
	case "effect_trigger":
		return "#d9f4f1"
	case "overlapped_tail":
		return "#e3efff"
	case "physical_owner":
		return "#e2f5e9"
	case "shared_skip":
		return "url(#shared-hatch)"
	case "fresh_fallback":
		return "#fff0d8"
	default:
		return "#f5f6f8"
	}
}

func figureAnnotation(demo LatestDemo, line int) *LatestCodeAnnotation {
	for index := range demo.Annotations {
		annotation := &demo.Annotations[index]
		if line >= annotation.StartLine && line <= annotation.EndLine {
			return annotation
		}
	}
	return nil
}

func figureCodeRow(buffer *bytes.Buffer, x, y, width int, lineNumber int, source string, annotation *LatestCodeAnnotation) {
	fill := "#f7f8fa"
	if annotation != nil {
		fill = figureTone(annotation.Tone)
	}
	fmt.Fprintf(buffer, `<rect x="%d" y="%d" width="%d" height="43" rx="4" fill="%s" stroke="#d9dde5"/>`, x, y, width, fill)
	fmt.Fprintf(buffer, `<text x="%d" y="%d" class="ln">%02d</text><text x="%d" y="%d" class="code">%s</text>`, x+8, y+17, lineNumber, x+30, y+17, html.EscapeString(source))
	if annotation != nil {
		fmt.Fprintf(buffer, `<text x="%d" y="%d" class="note"><tspan class="note-label">%s</tspan> · %s</text>`, x+30, y+34, html.EscapeString(annotation.Label), html.EscapeString(annotation.Note))
	}
}

func figurePanelHeader(buffer *bytes.Buffer, x int, panel, title, subtitle string) {
	fmt.Fprintf(buffer, `<text x="%d" y="35" class="panel">%s</text><text x="%d" y="58" class="heading">%s</text><text x="%d" y="77" class="sub">%s</text>`, x, html.EscapeString(panel), x, html.EscapeString(title), x, html.EscapeString(subtitle))
}

func figureArrow(buffer *bytes.Buffer, x1, y1, x2, y2 int) {
	fmt.Fprintf(buffer, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#667085" stroke-width="1.5" marker-end="url(#arrow)"/>`, x1, y1, x2, y2)
}

func figureSourcePrefix(buffer *bytes.Buffer, demo LatestDemo, x int) {
	figurePanelHeader(buffer, x, "(a)", "Reach-gated source prefix", "Generation tail overlaps Host READ")
	lines := strings.Split(strings.TrimSuffix(demo.Source, "\n"), "\n")
	y := 94
	for index, line := range lines {
		figureCodeRow(buffer, x, y, 360, index+1, line, figureAnnotation(demo, index+1))
		y += 49
	}
	fmt.Fprintf(buffer, `<text x="%d" y="258" class="axis-title">MEASURED EXECUTION</text>`, x)
	for laneIndex, lane := range demo.Lanes {
		laneY := 282 + laneIndex*68
		fmt.Fprintf(buffer, `<text x="%d" y="%d" class="lane-label">%s</text>`, x, laneY, html.EscapeString(lane.Label))
		trackX := x + 112
		trackWidth := 248.0
		fmt.Fprintf(buffer, `<rect x="%d" y="%d" width="248" height="34" rx="3" fill="#f4f6f8" stroke="#d9dde5"/>`, trackX, laneY-14)
		for _, segment := range lane.Segments {
			segmentX := float64(trackX) + float64(segment.StartNS)/float64(lane.DurationNS)*trackWidth
			segmentWidth := float64(segment.EndNS-segment.StartNS) / float64(lane.DurationNS) * trackWidth
			segmentY := laneY - 11
			segmentHeight := 13
			fill := "#4a91ff"
			if segment.Tone == "effect" {
				segmentY = laneY + 4
				fill = "#21a99c"
			} else if segment.Tone == "finalize" {
				segmentY = laneY + 4
				fill = "#a9b2c0"
			}
			fmt.Fprintf(buffer, `<rect x="%.1f" y="%d" width="%.1f" height="%d" rx="2" fill="%s"/>`, segmentX, segmentY, segmentWidth, segmentHeight, fill)
		}
	}
	fmt.Fprintf(buffer, `<text x="%d" y="430" class="metric">%s → %s</text><text x="%d" y="450" class="metric-note">%s mechanism window · %s</text>`, x, html.EscapeString(demo.Metrics[0].Value), html.EscapeString(demo.Metrics[1].Value), x, html.EscapeString(demo.Metrics[2].Value), html.EscapeString(demo.Metrics[2].Note))
}

func figureSharing(buffer *bytes.Buffer, demo LatestDemo, x int) {
	figurePanelHeader(buffer, x, "(b)", "Exact request sharing", "Logical requests share a sealed physical Guest")
	lines := strings.Split(demo.Source, "\n")
	fmt.Fprintf(buffer, `<text x="%d" y="103" class="actor">AGENT A</text>`, x)
	figureCodeRow(buffer, x, 111, 360, 2, lines[1], figureAnnotation(demo, 2))
	fmt.Fprintf(buffer, `<text x="%d" y="178" class="actor">AGENT B · IDENTICAL SEALED REQUEST</text>`, x)
	figureCodeRow(buffer, x, 186, 360, 5, lines[4], figureAnnotation(demo, 5))

	fmt.Fprintf(buffer, `<rect x="%d" y="302" width="92" height="38" rx="5" class="logical"/><text x="%d" y="325" class="box-text">logical A</text>`, x, x+19)
	fmt.Fprintf(buffer, `<rect x="%d" y="362" width="92" height="38" rx="5" class="logical"/><text x="%d" y="385" class="box-text">logical B</text>`, x, x+19)
	fmt.Fprintf(buffer, `<rect x="%d" y="327" width="142" height="48" rx="5" fill="#e2f5e9" stroke="#6eb68a"/><text x="%d" y="348" class="box-text">%s physical Guest</text><text x="%d" y="364" class="box-sub">%s</text>`, x+208, x+222, html.EscapeString(demo.Metrics[1].Value), x+214, html.EscapeString(demo.Facts[0].Value))
	figureArrow(buffer, x+92, 321, x+208, 343)
	figureArrow(buffer, x+92, 381, x+208, 359)
	fmt.Fprintf(buffer, `<text x="%d" y="430" class="metric">%s logical → %s physical</text><text x="%d" y="450" class="metric-note">%s oracle results accepted</text>`, x, html.EscapeString(demo.Metrics[0].Value), html.EscapeString(demo.Metrics[1].Value), x, html.EscapeString(demo.Metrics[2].Value))
}

func figureFallback(buffer *bytes.Buffer, demo LatestDemo, x int) {
	figurePanelHeader(buffer, x, "(c)", "Source mismatch fails closed", "Different identity keeps fresh execution")
	lines := strings.Split(demo.Source, "\n")
	fmt.Fprintf(buffer, `<text x="%d" y="103" class="actor">EXACT REQUEST</text>`, x)
	figureCodeRow(buffer, x, 111, 360, 2, lines[1], figureAnnotation(demo, 2))
	fmt.Fprintf(buffer, `<text x="%d" y="178" class="actor">SOURCE-MISMATCH REQUEST</text>`, x)
	fallbackAnnotation := figureAnnotation(demo, 5)
	figureCodeRow(buffer, x, 186, 360, 5, lines[4], fallbackAnnotation)

	fmt.Fprintf(buffer, `<rect x="%d" y="302" width="112" height="38" rx="5" class="logical"/><text x="%d" y="325" class="box-text">exact request</text>`, x, x+16)
	fmt.Fprintf(buffer, `<rect x="%d" y="362" width="112" height="38" rx="5" fill="#fff0d8" stroke="#d59a3b"/><text x="%d" y="385" class="box-text">source mismatch</text>`, x, x+10)
	fmt.Fprintf(buffer, `<rect x="%d" y="302" width="128" height="38" rx="5" fill="#e2f5e9" stroke="#6eb68a"/><text x="%d" y="325" class="box-text">physical Guest A</text>`, x+222, x+239)
	fmt.Fprintf(buffer, `<rect x="%d" y="362" width="128" height="38" rx="5" fill="#fff0d8" stroke="#d59a3b"/><text x="%d" y="385" class="box-text">%s</text>`, x+222, x+232, html.EscapeString(fallbackAnnotation.Label))
	figureArrow(buffer, x+112, 321, x+222, 321)
	figureArrow(buffer, x+112, 381, x+222, 381)
	fmt.Fprintf(buffer, `<text x="%d" y="430" class="metric">%s logical → %s physical</text><text x="%d" y="450" class="metric-note">unsafe reuse = %s · %s</text>`, x, html.EscapeString(demo.Metrics[0].Value), html.EscapeString(demo.Metrics[1].Value), x, html.EscapeString(demo.Metrics[2].Value), html.EscapeString(demo.Facts[0].Value))
}

func validatePaperFigureLayout(overlap, sharing, fallback LatestDemo) error {
	overlapLine1, overlapLine2 := figureAnnotation(overlap, 1), figureAnnotation(overlap, 2)
	if len(strings.Split(overlap.Source, "\n")) < 3 || len(overlap.Lanes) < 2 || len(overlap.Metrics) < 3 || overlap.Metrics[0].Label != "Generate first" || overlap.Metrics[1].Label != "Stream prefix" || overlap.Metrics[2].Label != "Mechanism window" || overlapLine1 == nil || overlapLine2 == nil || overlapLine1.Label == "" || overlapLine2.Label == "" {
		return errors.New("source-prefix paper figure layout contract drifted")
	}
	sharingLine2, sharingLine5 := figureAnnotation(sharing, 2), figureAnnotation(sharing, 5)
	if len(strings.Split(sharing.Source, "\n")) < 5 || len(sharing.Metrics) < 3 || len(sharing.Facts) == 0 || sharing.Metrics[0].Label != "Logical requests" || sharing.Metrics[1].Label != "Physical executions" || sharing.Metrics[2].Label != "Oracle results" || sharingLine2 == nil || sharingLine5 == nil || sharingLine2.Label == "" || sharingLine5.Label == "" {
		return errors.New("sharing paper figure layout contract drifted")
	}
	fallbackLine2, fallbackLine5 := figureAnnotation(fallback, 2), figureAnnotation(fallback, 5)
	if len(strings.Split(fallback.Source, "\n")) < 5 || len(fallback.Metrics) < 3 || len(fallback.Facts) == 0 || fallback.Metrics[0].Label != "Logical requests" || fallback.Metrics[1].Label != "Physical executions" || fallback.Metrics[2].Label != "Unsafe reuse" || fallbackLine2 == nil || fallbackLine5 == nil || fallbackLine2.Label == "" || fallbackLine5.Label == "" {
		return errors.New("fallback paper figure layout contract drifted")
	}
	return nil
}

func PaperFigureSVG(snapshot LatestSnapshot) ([]byte, error) {
	if err := ValidateLatestSnapshot(snapshot); err != nil {
		return nil, err
	}
	overlap, err := figureDemo(snapshot, "source-prefix-overlap")
	if err != nil {
		return nil, err
	}
	sharing, err := figureDemo(snapshot, "exact-request-sharing")
	if err != nil {
		return nil, err
	}
	fallback, err := figureDemo(snapshot, "source-mismatch-fallback")
	if err != nil {
		return nil, err
	}

	if err := validatePaperFigureLayout(overlap, sharing, fallback); err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	buffer.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="520" viewBox="0 0 1200 520" role="img" aria-labelledby="title desc"><title id="title">Pysolate code-centred execution decisions</title><desc id="desc">Reach-gated source overlap, exact request sharing, and source-mismatch fresh execution.</desc><defs><pattern id="shared-hatch" width="8" height="8" patternUnits="userSpaceOnUse"><rect width="8" height="8" fill="#f0ebff"/><path d="M-2,2 l4,-4 M0,8 l8,-8 M6,10 l4,-4" stroke="#a682ff" stroke-width="1"/></pattern><marker id="arrow" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto"><path d="M0,0 L7,3.5 L0,7 z" fill="#667085"/></marker><style>.panel{font:700 13px Arial,sans-serif;fill:#344054}.heading{font:700 17px Arial,sans-serif;fill:#101828}.sub{font:11px Arial,sans-serif;fill:#667085}.actor,.axis-title{font:700 9px Arial,sans-serif;letter-spacing:.7px;fill:#475467}.code{font:10px ui-monospace,SFMono-Regular,Menlo,monospace;fill:#1d2939}.ln{font:9px ui-monospace,monospace;fill:#98a2b3}.note{font:8.2px Arial,sans-serif;fill:#475467}.note-label{font-weight:700;fill:#344054}.lane-label{font:9px Arial,sans-serif;fill:#475467}.metric{font:700 17px Arial,sans-serif;fill:#101828}.metric-note{font:10px Arial,sans-serif;fill:#667085}.logical{fill:#f5f7fa;stroke:#98a2b3}.box-text{font:700 10px Arial,sans-serif;fill:#344054}.box-sub{font:9px Arial,sans-serif;fill:#667085}.legend{font:10px Arial,sans-serif;fill:#475467}</style></defs><rect width="1200" height="520" fill="#ffffff"/>`)
	figureSourcePrefix(&buffer, overlap, 20)
	buffer.WriteString(`<line x1="395" y1="22" x2="395" y2="472" stroke="#e4e7ec"/>`)
	figureSharing(&buffer, sharing, 420)
	buffer.WriteString(`<line x1="795" y1="22" x2="795" y2="472" stroke="#e4e7ec"/>`)
	figureFallback(&buffer, fallback, 820)
	buffer.WriteString(`<rect x="20" y="486" width="13" height="13" rx="2" fill="#d9f4f1" stroke="#7ccdc4"/><text x="39" y="497" class="legend">effect reached</text><rect x="145" y="486" width="13" height="13" rx="2" fill="#e3efff" stroke="#8eb7ee"/><text x="164" y="497" class="legend">overlapped source tail</text><rect x="318" y="486" width="13" height="13" rx="2" fill="#e2f5e9" stroke="#6eb68a"/><text x="337" y="497" class="legend">physical owner</text><rect x="460" y="486" width="13" height="13" rx="2" fill="url(#shared-hatch)" stroke="#a682ff"/><text x="479" y="497" class="legend">shared logical request; duplicate physical run skipped</text><rect x="790" y="486" width="13" height="13" rx="2" fill="#fff0d8" stroke="#d59a3b"/><text x="809" y="497" class="legend">fresh execution after identity rejection</text></svg>`)

	decoder := xml.NewDecoder(bytes.NewReader(buffer.Bytes()))
	for {
		_, err := decoder.Token()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		return nil, fmt.Errorf("generated paper figure is invalid SVG: %w", err)
	}
	if strings.Contains(strings.ToLower(buffer.String()), "cached") {
		return nil, errors.New("paper figure must not mislabel exact sharing as cache")
	}
	return buffer.Bytes(), nil
}
