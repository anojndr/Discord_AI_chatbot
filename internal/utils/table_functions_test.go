package utils

import (
	"strings"
	"testing"
)

func TestMarkdownTableParsing(t *testing.T) {
	converter := NewRodTableConverter()

	markdownTable := `| Name | Age | City |
|------|-----|------|
| Alice | 30 | New York |
| Bob | 25 | London |
| Charlie | 35 | Tokyo |`

	tableData, err := converter.parseMarkdownTable(markdownTable)
	if err != nil {
		t.Fatalf("Failed to parse markdown table: %v", err)
	}

	// Verify headers
	expectedHeaders := []string{"Name", "Age", "City"}
	if len(tableData.Headers) != len(expectedHeaders) {
		t.Errorf("Expected %d headers, got %d", len(expectedHeaders), len(tableData.Headers))
	}

	for i, expected := range expectedHeaders {
		if i < len(tableData.Headers) && tableData.Headers[i] != expected {
			t.Errorf("Expected header[%d] to be '%s', got '%s'", i, expected, tableData.Headers[i])
		}
	}

	// Verify rows
	expectedRows := [][]string{
		{"Alice", "30", "New York"},
		{"Bob", "25", "London"},
		{"Charlie", "35", "Tokyo"},
	}

	if len(tableData.Rows) != len(expectedRows) {
		t.Errorf("Expected %d rows, got %d", len(expectedRows), len(tableData.Rows))
	}

	for i, expectedRow := range expectedRows {
		if i < len(tableData.Rows) {
			actualRow := tableData.Rows[i]
			if len(actualRow) != len(expectedRow) {
				t.Errorf("Row %d: expected %d columns, got %d", i, len(expectedRow), len(actualRow))
				continue
			}
			for j, expectedCell := range expectedRow {
				if j < len(actualRow) && actualRow[j] != expectedCell {
					t.Errorf("Row %d, Column %d: expected '%s', got '%s'", i, j, expectedCell, actualRow[j])
				}
			}
		}
	}
}

func TestHTMLGeneration(t *testing.T) {
	converter := NewRodTableConverter()

	tableData := &TableData{
		Headers: []string{"Product", "Price", "Stock"},
		Rows: [][]string{
			{"Laptop", "$999", "15"},
			{"Phone", "$599", "32"},
		},
	}

	html := converter.generateTableHTML(tableData)

	// Check that HTML contains expected elements
	if !strings.Contains(html, "<table>") {
		t.Error("Generated HTML does not contain table tag")
	}

	if !strings.Contains(html, "<thead>") {
		t.Error("Generated HTML does not contain thead tag")
	}

	if !strings.Contains(html, "<tbody>") {
		t.Error("Generated HTML does not contain tbody tag")
	}

	// Check headers
	for _, header := range tableData.Headers {
		if !strings.Contains(html, header) {
			t.Errorf("Generated HTML does not contain header '%s'", header)
		}
	}

	// Check row data
	for _, row := range tableData.Rows {
		for _, cell := range row {
			if !strings.Contains(html, cell) {
				t.Errorf("Generated HTML does not contain cell data '%s'", cell)
			}
		}
	}
}

func TestHTMLEscaping(t *testing.T) {
	converter := NewRodTableConverter()

	testCases := []struct {
		input    string
		expected string
	}{
		{"<script>", "&lt;script&gt;"},
		{"A & B", "A &amp; B"},
		{"'quote'", "&#39;quote&#39;"},
		{"\"double\"", "&quot;double&quot;"},
	}

	for _, tc := range testCases {
		result := converter.escapeHTML(tc.input)
		if result != tc.expected {
			t.Errorf("escapeHTML('%s'): expected '%s', got '%s'", tc.input, tc.expected, result)
		}
	}
}

func TestTableRendererConfiguration(t *testing.T) {
	// Test default renderer (using gg graphics)
	defaultRenderer := NewTableRenderer()
	if defaultRenderer.useRod {
		t.Error("Default renderer should not use Rod")
	}
	if defaultRenderer.rodConverter != nil {
		t.Error("Default renderer should not have Rod converter")
	}

	// Test Rod renderer
	rodRenderer := NewTableRendererWithRod()
	if !rodRenderer.useRod {
		t.Error("Rod renderer should use Rod")
	}
	if rodRenderer.rodConverter == nil {
		t.Error("Rod renderer should have Rod converter")
	}
}

func TestTableDetection(t *testing.T) {
	renderer := NewTableRenderer()

	content := `Here is some text.

| Product | Price | Stock |
|---------|-------|-------|
| Laptop | $999 | 15 |
| Phone | $599 | 32 |

And some more text.

| Name | Age |
|------|-----|
| Alice | 30 |
| Bob | 25 |

End of content.`

	tables := renderer.DetectTables(content)
	
	if len(tables) != 2 {
		t.Errorf("Expected 2 tables, found %d", len(tables))
	}

	// Check first table
	if len(tables) > 0 {
		firstTable := tables[0]
		if firstTable.RowCount != 3 {
			t.Errorf("First table: expected 3 rows, got %d", firstTable.RowCount)
		}
		if firstTable.ColCount != 3 {
			t.Errorf("First table: expected 3 columns, got %d", firstTable.ColCount)
		}
	}

	// Check second table
	if len(tables) > 1 {
		secondTable := tables[1]
		if secondTable.RowCount != 3 {
			t.Errorf("Second table: expected 3 rows, got %d", secondTable.RowCount)
		}
		if secondTable.ColCount != 2 {
			t.Errorf("Second table: expected 2 columns, got %d", secondTable.ColCount)
		}
	}
}