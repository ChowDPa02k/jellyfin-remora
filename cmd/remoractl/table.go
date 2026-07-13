package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

type columnWidth struct {
	min int
	max int
}

func writeStatus(w io.Writer, status model.Status, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	}
	_, err := io.WriteString(w, renderStatus(status))
	return err
}

func renderStatus(status model.Status) string {
	var output strings.Builder
	uid := "-"
	if status.Username != "" {
		uid = status.Username
		if status.UID >= 0 {
			uid = fmt.Sprintf("%d (%s)", status.UID, status.Username)
		}
	} else if status.UID > 0 {
		uid = strconv.Itoa(status.UID)
	}
	pid := "-"
	if status.PID > 0 {
		pid = strconv.Itoa(status.PID)
	}
	ports := "-"
	if len(status.Ports) > 0 {
		values := make([]string, len(status.Ports))
		for i, port := range status.Ports {
			values[i] = strconv.Itoa(port)
		}
		ports = strings.Join(values, ", ")
	}
	summary := [][]string{
		{"UID", uid},
		{"PID", pid},
		{"Executable Path", fallback(status.Executable)},
		{"Version", fallback(status.Version)},
		{"Server Name", fallback(status.ServerName)},
		{"Port", ports},
		{"State", string(status.State)},
		{"Desired State", string(status.DesiredState)},
		{"Uptime", formatUptime(status.UptimeSeconds)},
	}
	if status.LastError != "" {
		summary = append(summary, []string{"Detail", status.LastError})
	}
	output.WriteString(renderTable("Jellyfin Status", nil, summary, []columnWidth{{min: 25, max: 25}, {min: 52, max: 80}}))
	output.WriteByte('\n')

	hasStorageDetail := false
	for _, storage := range status.Storage {
		hasStorageDetail = hasStorageDetail || storage.Message != ""
	}
	storageHeaders := []string{"#", "healthy", "type", "target"}
	storageWidths := []columnWidth{{min: 1, max: 4}, {min: 7, max: 7}, {min: 8, max: 10}, {min: 52, max: 72}}
	if hasStorageDetail {
		storageHeaders = append(storageHeaders, "detail")
		storageWidths = append(storageWidths, columnWidth{min: 20, max: 52})
	}
	storageRows := make([][]string, 0, len(status.Storage))
	for _, storage := range status.Storage {
		typeName := storage.Type
		if typeName == "smb" {
			typeName = "samba"
		}
		row := []string{strconv.Itoa(storage.Index), strconv.FormatBool(storage.Healthy), typeName, storage.Target}
		if hasStorageDetail {
			row = append(row, storage.Message)
		}
		storageRows = append(storageRows, row)
	}
	output.WriteString(renderTable("Storage Volumes", storageHeaders, storageRows, storageWidths))
	output.WriteByte('\n')

	sessionRows := make([][]string, 0, len(status.Sessions))
	for _, session := range status.Sessions {
		sessionRows = append(sessionRows, []string{shortID(session.ID), session.Status, session.User, session.Device, session.Media})
	}
	output.WriteString(renderTable("Active Sessions", []string{"#", "status", "user", "device", "media"}, sessionRows, []columnWidth{{min: 8, max: 8}, {min: 7, max: 8}, {min: 10, max: 24}, {min: 25, max: 36}, {min: 28, max: 48}}))
	return output.String()
}

func renderTable(title string, headers []string, rows [][]string, specs []columnWidth) string {
	widths := make([]int, len(specs))
	for i, spec := range specs {
		widths[i] = spec.min
		if i < len(headers) {
			widths[i] = clamp(displayWidth(sanitizeCell(headers[i])), spec.min, spec.max)
		}
	}
	for _, row := range rows {
		for i := range widths {
			if i < len(row) {
				widths[i] = max(widths[i], clamp(displayWidth(sanitizeCell(row[i])), specs[i].min, specs[i].max))
			}
		}
	}
	totalWidth := 1
	for _, width := range widths {
		totalWidth += width + 3
	}
	if required := displayWidth(sanitizeCell(title)) + 4; required > totalWidth && len(widths) > 0 {
		widths[len(widths)-1] += required - totalWidth
		totalWidth = required
	}
	titleLine := strings.Repeat("-", totalWidth-2)
	line := tableLine(widths)
	var output strings.Builder
	output.WriteByte('+')
	output.WriteString(titleLine)
	output.WriteString("+\n")
	output.WriteString("| ")
	output.WriteString(padCell(truncateCell(title, totalWidth-4), totalWidth-4))
	output.WriteString(" |\n")
	output.WriteByte('+')
	output.WriteString(titleLine)
	output.WriteString("+\n")
	if len(headers) > 0 {
		output.WriteString(tableRow(headers, widths))
		output.WriteString(line)
	}
	for _, row := range rows {
		output.WriteString(tableRow(row, widths))
	}
	output.WriteString(line)
	return output.String()
}

func tableLine(widths []int) string {
	var line strings.Builder
	line.WriteByte('+')
	for _, width := range widths {
		line.WriteString(strings.Repeat("-", width+2))
		line.WriteString("+")
	}
	line.WriteByte('\n')
	return line.String()
}

func tableRow(row []string, widths []int) string {
	var output strings.Builder
	output.WriteByte('|')
	for i, width := range widths {
		value := ""
		if i < len(row) {
			value = row[i]
		}
		value = truncateCell(value, width)
		output.WriteByte(' ')
		output.WriteString(padCell(value, width))
		output.WriteString(" |")
	}
	output.WriteByte('\n')
	return output.String()
}

func sanitizeCell(value string) string {
	var output strings.Builder
	for _, r := range strings.ToValidUTF8(value, "�") {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			output.WriteByte(' ')
		case unicode.IsControl(r):
			continue
		default:
			output.WriteRune(r)
		}
	}
	return strings.TrimSpace(output.String())
}

func displayWidth(value string) int {
	width := 0
	for _, r := range value {
		width += runeWidth(r)
	}
	return width
}

func runeWidth(r rune) int {
	if r == 0 || unicode.IsControl(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) || (r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) || (r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) || (r >= 0x1f300 && r <= 0x1faff) ||
		(r >= 0x20000 && r <= 0x3fffd)) {
		return 2
	}
	return 1
}

func truncateCell(value string, width int) string {
	value = sanitizeCell(value)
	if displayWidth(value) <= width {
		return value
	}
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return "…"
	}
	var output bytes.Buffer
	used := 0
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		rw := runeWidth(r)
		if used+rw > width-1 {
			break
		}
		output.WriteRune(r)
		used += rw
		value = value[size:]
	}
	output.WriteRune('…')
	return output.String()
}

func padCell(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-displayWidth(value)))
}

func shortID(value string) string {
	value = sanitizeCell(value)
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func fallback(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func formatUptime(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	return (time.Duration(seconds) * time.Second).String()
}

func clamp(value, minimum, maximum int) int {
	return min(maximum, max(minimum, value))
}
