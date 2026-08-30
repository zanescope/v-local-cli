package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode/utf8"
)

type outputMode string

const (
	outputJSON  outputMode = "json"
	outputYAML  outputMode = "yaml"
	outputTable outputMode = "table"
)

func parseOutputMode(args []string) ([]string, outputMode, error) {
	if len(args) == 0 {
		return args, outputJSON, nil
	}
	value := ""
	remaining := args
	switch {
	case args[0] == "--output":
		if len(args) < 2 {
			return nil, outputJSON, errors.New("--output 缺少格式")
		}
		value, remaining = args[1], args[2:]
	case strings.HasPrefix(args[0], "--output="):
		value, remaining = strings.TrimPrefix(args[0], "--output="), args[1:]
	default:
		return args, outputJSON, nil
	}
	mode := outputMode(strings.ToLower(strings.TrimSpace(value)))
	if mode != outputJSON && mode != outputYAML && mode != outputTable {
		return nil, outputJSON, errors.New("--output 只能为 json、yaml 或 table")
	}
	return remaining, mode, nil
}

func normalizedOutput(value any) any {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var result any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if decoder.Decode(&result) != nil {
		return nil
	}
	return result
}

func yamlScalar(value any) string {
	switch current := value.(type) {
	case nil:
		return "null"
	case bool:
		if current {
			return "true"
		}
		return "false"
	case json.Number:
		return current.String()
	case string:
		payload, _ := json.Marshal(current)
		return string(payload)
	default:
		payload, _ := json.Marshal(current)
		return string(payload)
	}
}

func writeYAMLNode(writer io.Writer, value any, indent int) {
	padding := strings.Repeat(" ", indent)
	switch current := value.(type) {
	case map[string]any:
		if len(current) == 0 {
			_, _ = io.WriteString(writer, "{}\n")
			return
		}
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			_, _ = io.WriteString(writer, padding+yamlScalar(key)+":")
			child := current[key]
			switch typed := child.(type) {
			case map[string]any:
				if len(typed) == 0 {
					_, _ = io.WriteString(writer, " {}\n")
				} else {
					_, _ = io.WriteString(writer, "\n")
					writeYAMLNode(writer, child, indent+2)
				}
			case []any:
				if len(typed) == 0 {
					_, _ = io.WriteString(writer, " []\n")
				} else {
					_, _ = io.WriteString(writer, "\n")
					writeYAMLNode(writer, child, indent+2)
				}
			default:
				_, _ = io.WriteString(writer, " "+yamlScalar(child)+"\n")
			}
		}
	case []any:
		if len(current) == 0 {
			_, _ = io.WriteString(writer, padding+"[]\n")
			return
		}
		for _, child := range current {
			_, _ = io.WriteString(writer, padding+"-")
			switch typed := child.(type) {
			case map[string]any:
				if len(typed) == 0 {
					_, _ = io.WriteString(writer, " {}\n")
				} else {
					_, _ = io.WriteString(writer, "\n")
					writeYAMLNode(writer, child, indent+2)
				}
			case []any:
				if len(typed) == 0 {
					_, _ = io.WriteString(writer, " []\n")
				} else {
					_, _ = io.WriteString(writer, "\n")
					writeYAMLNode(writer, child, indent+2)
				}
			default:
				_, _ = io.WriteString(writer, " "+yamlScalar(child)+"\n")
			}
		}
	default:
		_, _ = io.WriteString(writer, padding+yamlScalar(current)+"\n")
	}
}

func tableCell(value any) string {
	if value == nil {
		return ""
	}
	var result string
	switch current := value.(type) {
	case string:
		result = current
	case json.Number:
		result = current.String()
	case bool:
		result = strconv.FormatBool(current)
	default:
		payload, _ := json.Marshal(current)
		result = string(payload)
	}
	result = strings.Join(strings.Fields(strings.ToValidUTF8(result, "�")), " ")
	if utf8.RuneCountInString(result) > 160 {
		runes := []rune(result)
		result = string(runes[:159]) + "…"
	}
	return result
}

func tableColumns(rows []map[string]any) []string {
	seen := map[string]bool{}
	var scalar []string
	for _, row := range rows {
		for key, value := range row {
			switch value.(type) {
			case nil, string, json.Number, bool:
				seen[key] = true
			}
		}
	}
	preferred := []string{
		"username", "display", "kind", "snapshot_unread_count", "last_timestamp",
		"timestamp", "sender", "chat", "title", "content", "text", "evidence_id",
	}
	for _, key := range preferred {
		if seen[key] {
			scalar = append(scalar, key)
			delete(seen, key)
		}
	}
	remaining := make([]string, 0, len(seen))
	for key := range seen {
		remaining = append(remaining, key)
	}
	sort.Strings(remaining)
	scalar = append(scalar, remaining...)
	if len(scalar) > 12 {
		scalar = scalar[:12]
	}
	return scalar
}

func writeRowsTable(writer io.Writer, rows []map[string]any) {
	if len(rows) == 0 {
		_, _ = io.WriteString(writer, "(no rows)\n")
		return
	}
	columns := tableColumns(rows)
	if len(columns) == 0 {
		_, _ = io.WriteString(writer, "(rows contain only structured values; use --output yaml)\n")
		return
	}
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	for index, column := range columns {
		if index > 0 {
			_, _ = io.WriteString(table, "\t")
		}
		_, _ = io.WriteString(table, strings.ToUpper(column))
	}
	_, _ = io.WriteString(table, "\n")
	for _, row := range rows {
		for index, column := range columns {
			if index > 0 {
				_, _ = io.WriteString(table, "\t")
			}
			_, _ = io.WriteString(table, tableCell(row[column]))
		}
		_, _ = io.WriteString(table, "\n")
	}
	_ = table.Flush()
}

func writeTable(writer io.Writer, value envelope) {
	if value.CommandStatus != "succeeded" {
		table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
		_, _ = io.WriteString(table, "TYPE\tMESSAGE\tHINT\n")
		if value.Error != nil {
			_, _ = fmt.Fprintf(table, "%s\t%s\t%s\n", tableCell(value.Error.Type), tableCell(value.Error.Message), tableCell(value.Error.Hint))
		}
		_ = table.Flush()
		return
	}
	normalized, _ := normalizedOutput(value.Data).(map[string]any)
	if normalized == nil {
		_, _ = io.WriteString(writer, tableCell(normalizedOutput(value.Data))+"\n")
		return
	}
	if rawItems, found := normalized["items"].([]any); found {
		rows := make([]map[string]any, 0, len(rawItems))
		for _, raw := range rawItems {
			if row, ok := raw.(map[string]any); ok {
				if message, found := row["message"].(map[string]any); found {
					message["change_kind"] = row["change_kind"]
					row = message
				}
				rows = append(rows, row)
			}
		}
		writeRowsTable(writer, rows)
	} else {
		keys := make([]string, 0, len(normalized))
		for key := range normalized {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
		_, _ = io.WriteString(table, "FIELD\tVALUE\n")
		for _, key := range keys {
			_, _ = fmt.Fprintf(table, "%s\t%s\n", key, tableCell(normalized[key]))
		}
		_ = table.Flush()
	}
	if value.Meta != nil {
		generation := tableCell(value.Meta["generation_id"])
		snapshot := tableCell(value.Meta["snapshot_created_at"])
		if generation != "" || snapshot != "" {
			_, _ = fmt.Fprintf(writer, "# generation_id=%s snapshot_created_at=%s\n", generation, snapshot)
		}
	}
}

func writeEnvelope(writer io.Writer, value envelope, mode outputMode) {
	if value.Meta == nil {
		value.Meta = map[string]any{}
	}
	if mode != outputJSON {
		value.Meta["output_format"] = string(mode)
	}
	switch mode {
	case outputYAML:
		writeYAMLNode(writer, normalizedOutput(value), 0)
	case outputTable:
		writeTable(writer, value)
	default:
		writeJSON(writer, value)
	}
}

func writeErrorMode(writer io.Writer, err *commandError, mode outputMode) {
	meta := map[string]any{
		"version": Version, "runtime": "go", "snapshot_created_at": nil, "snapshot_age_seconds": nil,
	}
	for name, value := range err.meta {
		meta[name] = value
	}
	writeEnvelope(writer, envelope{SchemaVersion: responseSchemaVersion, CommandStatus: "failed", Error: &errorValue{
		Type: err.typeName, Message: err.message, Hint: err.hint, Details: err.details,
	}, Meta: meta}, mode)
}
