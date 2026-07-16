package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"golang.org/x/term"
)

func writeStatus(w io.Writer, status model.Status, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	}
	_, err := io.WriteString(w, renderStatusStyled(status, statusColorEnabled(w)))
	return err
}

func writeEvents(w io.Writer, events []model.Event, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(events)
	}
	tw := newTable("Remora Events", []table.ColumnConfig{
		column(1, 6, 10),
		column(2, 20, 30),
		column(3, 16, 20),
		column(4, 18, 24),
		column(5, 24, 72),
	})
	tw.AppendHeader(table.Row{"#", "time", "type", "state", "message"})
	for _, event := range events {
		tw.AppendRow(sanitizeRow(table.Row{event.Sequence, event.Timestamp.Local().Format(time.RFC3339), event.Type, event.State, event.Message}))
	}
	_, err := io.WriteString(w, tw.Render()+"\n")
	return err
}

func writeAPIKeys(w io.Writer, keys []model.APIKey, jsonOutput bool) error {
	if jsonOutput {
		return writeIndentedJSON(w, keys)
	}
	tw := newTable("Jellyfin API Keys", []table.ColumnConfig{column(1, 16, 16), column(2, 24, 48), column(3, 6, 8), column(4, 8, 10)})
	tw.AppendHeader(table.Row{"id", "name", "active", "remora"})
	for _, key := range keys {
		tw.AppendRow(sanitizeRow(table.Row{key.ID, key.Name, key.Active, key.IsRemora}))
	}
	_, err := io.WriteString(w, tw.Render()+"\n")
	return err
}

func writeSessions(w io.Writer, sessions []model.Session, jsonOutput bool) error {
	if jsonOutput {
		return writeIndentedJSON(w, sessions)
	}
	if len(sessions) == 0 {
		_, err := io.WriteString(w, "No active sessions.\n")
		return err
	}
	_, err := io.WriteString(w, renderSessions(sessions)+"\n")
	return err
}

func renderStatus(status model.Status) string {
	return renderStatusStyled(status, false)
}

func renderStatusStyled(status model.Status, color bool) string {
	tables := []string{renderSummary(status, color), renderStorage(status, color)}
	if len(status.Sessions) > 0 {
		tables = append(tables, renderSessions(status.Sessions))
	}
	return strings.Join(tables, "\n\n") + "\n"
}

func renderSummary(status model.Status, color bool) string {
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

	rows := []table.Row{
		{"UID", uid},
		{"PID", pid},
		{"Executable Path", fallback(status.Executable)},
		{"Version", fallback(status.Version)},
		{"Server Name", fallback(status.ServerName)},
		{"Port", ports},
		{"State", string(status.State)},
		{"Database", databaseStatus(status.Database)},
		{"Desired State", string(status.DesiredState)},
		{"Uptime", formatUptime(status.UptimeSeconds)},
		{"FFmpeg Processes", status.FFmpegProcesses},
		{"Active Transcodes", status.ActiveTranscodes},
	}
	if !status.ProcessStarted.IsZero() {
		rows = append(rows, table.Row{"Process Started", status.ProcessStarted.Local().Format(time.RFC3339)})
	}
	if len(status.PlayingUsers) > 0 {
		rows = append(rows, table.Row{"Playing Users", strings.Join(status.PlayingUsers, ", ")})
	}
	if status.LastError != "" {
		rows = append(rows, table.Row{"Detail", status.LastError})
	}

	tw := newTable("Jellyfin Status", []table.ColumnConfig{
		column(1, 25, 25),
		column(2, 52, 80),
	})
	for _, row := range rows {
		clean := sanitizeRow(row)
		if len(clean) > 1 && clean[0] == "State" {
			clean[1] = colorState(fmt.Sprint(clean[1]), status.State, color)
		}
		if len(clean) > 1 && clean[0] == "Database" {
			clean[1] = colorDatabase(fmt.Sprint(clean[1]), status.Database, color)
		}
		tw.AppendRow(clean)
	}
	return tw.Render()
}

func renderStorage(status model.Status, color bool) string {
	hasDetail := false
	for _, storage := range status.Storage {
		hasDetail = hasDetail || storage.Message != ""
	}

	headings := table.Row{"#", "healthy", "type", "target"}
	columns := []table.ColumnConfig{
		column(1, 1, 4),
		column(2, 7, 7),
		column(3, 8, 10),
		unboundedColumn(4, 44),
	}
	if hasDetail {
		headings = append(headings, "detail")
		columns = append(columns, column(5, 20, 52))
	}

	tw := newTable("Storage Volumes", columns)
	tw.AppendHeader(sanitizeRow(headings))
	for _, storage := range status.Storage {
		typeName := storage.Type
		if typeName == "smb" {
			typeName = "samba"
		}
		row := sanitizeRow(table.Row{storage.Index, storage.Healthy, typeName, storage.Target})
		row[1] = colorHealthy(fmt.Sprint(row[1]), storage.Healthy, color)
		if hasDetail {
			row = append(row, sanitizeCell(storage.Message))
		}
		tw.AppendRow(row)
	}
	return tw.Render()
}

func renderSessions(sessions []model.Session) string {
	tw := newTable("Active Sessions", []table.ColumnConfig{
		column(1, 8, 8),
		column(2, 7, 8),
		column(3, 10, 24),
		column(4, 25, 36),
		column(5, 28, 48),
	})
	tw.AppendHeader(table.Row{"#", "status", "user", "device", "media"})
	for _, session := range sessions {
		tw.AppendRow(sanitizeRow(table.Row{shortID(session.ID), session.Status, session.User, session.Device, session.Media}))
	}
	return tw.Render()
}

func newTable(title string, columns []table.ColumnConfig) table.Writer {
	tw := table.NewWriter()
	style := table.StyleDefault
	style.Format.Header = text.FormatDefault
	style.Format.Footer = text.FormatDefault
	style.Format.Row = text.FormatDefault
	style.Title.Format = text.FormatDefault
	tw.SetStyle(style)
	tw.SetTitle(title)
	tw.SetColumnConfigs(columns)
	return tw
}

func column(number, minimum, maximum int) table.ColumnConfig {
	return table.ColumnConfig{
		Number:           number,
		WidthMin:         minimum,
		WidthMax:         maximum,
		WidthMaxEnforcer: snipCell,
	}
}

func unboundedColumn(number, minimum int) table.ColumnConfig {
	return table.ColumnConfig{Number: number, WidthMin: minimum}
}

func statusColorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fd, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(fd.Fd()))
}

func colorState(value string, state model.State, enabled bool) string {
	if !enabled {
		return value
	}
	colors := text.Colors{text.Bold, text.FgYellow}
	switch state {
	case model.StateRunning:
		colors = text.Colors{text.Bold, text.FgGreen}
	case model.StateStorageFenced, model.StateProcessFailed, model.StateDatabaseDamaged:
		colors = text.Colors{text.Bold, text.FgRed}
	case model.StateStopped, model.StateInit:
		colors = text.Colors{text.FgHiBlack}
	}
	return text.Escape(value, colors.EscapeSeq())
}

func databaseStatus(result model.DatabaseResult) string {
	if result.Damaged {
		return "damaged"
	}
	if result.Suspected {
		return "suspected"
	}
	return "healthy"
}

func colorDatabase(value string, result model.DatabaseResult, enabled bool) string {
	if !enabled {
		return value
	}
	color := text.FgGreen
	if result.Suspected {
		color = text.FgYellow
	}
	if result.Damaged {
		color = text.FgRed
	}
	return text.Escape(value, text.Colors{text.Bold, color}.EscapeSeq())
}

func colorHealthy(value string, healthy, enabled bool) string {
	if !enabled {
		return value
	}
	color := text.FgRed
	if healthy {
		color = text.FgGreen
	}
	return text.Escape(value, text.Colors{text.Bold, color}.EscapeSeq())
}

func snipCell(value string, width int) string {
	return text.Snip(value, width, "…")
}

func appendSanitizedRows(tw table.Writer, rows []table.Row) {
	for _, row := range rows {
		tw.AppendRow(sanitizeRow(row))
	}
}

func sanitizeRow(row table.Row) table.Row {
	clean := make(table.Row, len(row))
	for i, value := range row {
		clean[i] = sanitizeCell(fmt.Sprint(value))
	}
	return clean
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
