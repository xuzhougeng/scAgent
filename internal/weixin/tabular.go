package weixin

import (
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const (
	tabularPreviewRows      = 10
	tabularPreviewCols      = 5
	tabularHeaderDetectRows = 10
	tabularCellMaxLen       = 60
)

type tabularSummaryOptions struct {
	PreviewRows      int
	PreviewCols      int
	HeaderDetectRows int
}

func summarizeDelimitedFile(filename string, payload []byte) string {
	return summarizeDelimitedFileWithOptions(filename, payload, tabularSummaryOptions{
		PreviewRows:      tabularPreviewRows,
		PreviewCols:      tabularPreviewCols,
		HeaderDetectRows: tabularHeaderDetectRows,
	})
}

func summarizeDelimitedFileWithOptions(filename string, payload []byte, options tabularSummaryOptions) string {
	options = normalizeTabularSummaryOptions(options)
	text := strings.ToValidUTF8(string(payload), "\uFFFD")
	if strings.TrimSpace(text) == "" {
		return fmt.Sprintf("表格文件 %s 为空。", filename)
	}

	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = detectDelimiter(filename, text)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	var rowCount int
	var colCount int
	sampleRows := make([][]string, 0, maxInt(options.HeaderDetectRows, options.PreviewRows+1))

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return summarizeDelimitedFallback(filename, text, options)
		}
		if !recordHasContent(record) {
			continue
		}
		rowCount++
		if len(record) > colCount {
			colCount = len(record)
		}
		if len(sampleRows) < cap(sampleRows) {
			sampleRows = append(sampleRows, cloneRecord(record))
		}
	}

	if rowCount == 0 {
		return fmt.Sprintf("表格文件 %s 为空。", filename)
	}

	return buildTabularSummary(filename, rowCount, colCount, sampleRows, options)
}

func summarizeDelimitedFallback(filename, text string, options tabularSummaryOptions) string {
	lines := strings.Split(text, "\n")
	var colCount int
	rowCount := 0
	sampleRows := make([][]string, 0, maxInt(options.HeaderDetectRows, options.PreviewRows+1))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		rowCount++
		fields := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == '\t' })
		if len(fields) > colCount {
			colCount = len(fields)
		}
		if len(sampleRows) < cap(sampleRows) {
			sampleRows = append(sampleRows, cloneRecord(fields))
		}
	}
	if rowCount == 0 {
		return fmt.Sprintf("表格文件 %s 为空。", filename)
	}
	return buildTabularSummary(filename, rowCount, colCount, sampleRows, options)
}

func buildTabularSummary(filename string, rowCount, colCount int, sampleRows [][]string, options tabularSummaryOptions) string {
	displayCols := minInt(colCount, options.PreviewCols)
	headerDetected := detectHeaderRow(sampleRows, minInt(displayCols, maxRecordWidth(sampleRows)), options.HeaderDetectRows)
	previewStart := 0
	if headerDetected {
		previewStart = 1
	}

	preview := make([]string, 0, options.PreviewRows)
	for index := previewStart; index < len(sampleRows) && len(preview) < options.PreviewRows; index++ {
		if !recordHasContent(sampleRows[index]) {
			continue
		}
		preview = append(preview, summarizeRecord(sampleRows[index], displayCols))
	}

	summary := fmt.Sprintf("表格文件 %s：共 %d 行，%d 列。", filename, rowCount, colCount)
	if displayCols > 0 {
		if headerDetected {
			summary += fmt.Sprintf(" 检测到列名（前 %d 列）：%s。", displayCols, strings.Join(summarizeCells(sampleRows[0], displayCols), " | "))
		} else {
			summary += fmt.Sprintf(" 未可靠检测到列名，以下按前 %d 列预览。", displayCols)
		}
	}
	if len(preview) > 0 && displayCols > 0 {
		summary += fmt.Sprintf(" 前 %d 行预览（前 %d 列", len(preview), displayCols)
		if headerDetected {
			summary += "，不含表头"
		}
		summary += "）："
		summary += strings.Join(preview, " ; ")
	}
	return summary
}

func summarizeRecord(record []string, previewCols int) string {
	cells := summarizeCells(record, previewCols)
	return strings.Join(cells, " | ")
}

func summarizeCells(record []string, previewCols int) []string {
	limit := len(record)
	if limit > previewCols {
		limit = previewCols
	}
	cells := make([]string, 0, limit)
	for _, cell := range record[:limit] {
		value := cleanCell(cell)
		if len([]rune(value)) > tabularCellMaxLen {
			value = string([]rune(value)[:tabularCellMaxLen]) + "..."
		}
		if value == "" {
			value = "(empty)"
		}
		cells = append(cells, value)
	}
	return cells
}

func detectDelimiter(filename, text string) rune {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".tsv":
		return '\t'
	case ".csv":
		return ','
	}
	firstLine := text
	if index := strings.IndexByte(firstLine, '\n'); index >= 0 {
		firstLine = firstLine[:index]
	}
	if strings.Count(firstLine, "\t") > strings.Count(firstLine, ",") {
		return '\t'
	}
	return ','
}

func normalizeTabularSummaryOptions(options tabularSummaryOptions) tabularSummaryOptions {
	if options.PreviewRows <= 0 {
		options.PreviewRows = tabularPreviewRows
	}
	if options.PreviewCols <= 0 {
		options.PreviewCols = tabularPreviewCols
	}
	if options.HeaderDetectRows <= 1 {
		options.HeaderDetectRows = tabularHeaderDetectRows
	}
	return options
}

func detectHeaderRow(records [][]string, previewCols, sampleRows int) bool {
	if len(records) < 2 || previewCols <= 0 {
		return false
	}
	if sampleRows > len(records) {
		sampleRows = len(records)
	}

	score := 0
	comparableCols := 0
	headerLikeCols := 0
	headerCells := make([]string, 0, previewCols)

	for col := 0; col < previewCols; col++ {
		header := cleanCell(cellAt(records[0], col))
		if header == "" {
			continue
		}
		comparableCols++
		headerCells = append(headerCells, strings.ToLower(header))

		if looksLikeHeaderLabel(header) {
			score++
			headerLikeCols++
		}

		headerShape := tabularCellShape(header)
		sameShape := 0
		diffShape := 0
		for row := 1; row < sampleRows; row++ {
			value := cleanCell(cellAt(records[row], col))
			if value == "" {
				continue
			}
			if strings.EqualFold(value, header) {
				sameShape += 2
				continue
			}
			if tabularCellShape(value) == headerShape {
				sameShape++
			} else {
				diffShape++
			}
		}

		switch {
		case diffShape > sameShape:
			score += 2
		case sameShape > diffShape && sameShape > 0:
			score--
		}
	}

	if comparableCols == 0 {
		return false
	}
	if comparableCols >= 2 && headerLikeCols*2 >= comparableCols && stringsAreUnique(headerCells) {
		score++
	}
	return score > 0
}

func tabularCellShape(value string) string {
	value = cleanCell(value)
	if value == "" {
		return "empty"
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return "numeric"
	}

	hasLetter := false
	hasDigit := false
	hasLower := false
	hasUpper := false
	hasOther := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
			if unicode.IsLower(r) {
				hasLower = true
			}
			if unicode.IsUpper(r) {
				hasUpper = true
			}
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsSpace(r) || r == '_' || r == '-' || r == '/' || r == '.':
		default:
			hasOther = true
		}
	}

	switch {
	case hasOther:
		return "mixed_text"
	case hasLetter && hasDigit:
		return "alnum"
	case hasLetter && hasLower && !hasUpper:
		return "lower_text"
	case hasLetter && hasUpper && !hasLower:
		return "upper_text"
	case hasLetter:
		return "mixed_case_text"
	case hasDigit:
		return "digit_text"
	default:
		return "text"
	}
}

func looksLikeHeaderLabel(value string) bool {
	value = cleanCell(value)
	if len([]rune(value)) < 2 {
		return false
	}
	hasLetter := false
	sawDigit := false
	for _, r := range value {
		if unicode.IsLetter(r) {
			if sawDigit {
				return false
			}
			hasLetter = true
			continue
		}
		if unicode.IsDigit(r) {
			if !hasLetter {
				return false
			}
			sawDigit = true
		}
	}
	return hasLetter
}

func cleanCell(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(value, "\uFEFF"), "\r", " "), "\n", " "))
}

func recordHasContent(record []string) bool {
	for _, cell := range record {
		if cleanCell(cell) != "" {
			return true
		}
	}
	return false
}

func cloneRecord(record []string) []string {
	cloned := make([]string, len(record))
	copy(cloned, record)
	return cloned
}

func cellAt(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return record[index]
}

func stringsAreUnique(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func maxRecordWidth(records [][]string) int {
	maxWidth := 0
	for _, record := range records {
		if len(record) > maxWidth {
			maxWidth = len(record)
		}
	}
	return maxWidth
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
