package loaders

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"cloud.google.com/go/spanner/apiv1/spannerpb"
	"go.mercari.io/yo/models"
	"google.golang.org/api/iterator"
)

// mockRowData stores the data for a single row.
type mockRowData struct {
	OrdinalPosition       spanner.NullInt64
	ColumnName            string
	ErrorOnIteratorNext error
}

// mockRowIterator implements RowIteratorAPI.
type mockRowIterator struct {
	rows         []mockRowData
	currentIndex int
	err          error // An error to return from Next, if any for the whole iterator
	closed       bool  // Added field
}

// Next implements the RowIteratorAPI interface.
func (it *mockRowIterator) Next() (*spanner.Row, error) {
	if it.err != nil {
		return nil, it.err
	}
	if it.closed { // Prevent Next after Stop has been called
		return nil, iterator.Done
	}
	if it.currentIndex >= len(it.rows) {
		return nil, iterator.Done
	}
	rowData := it.rows[it.currentIndex]
	it.currentIndex++

	if rowData.ErrorOnIteratorNext != nil {
		return nil, rowData.ErrorOnIteratorNext
	}

	spannerCols := []string{"ORDINAL_POSITION", "COLUMN_NAME"}
	spannerVals := []interface{}{rowData.OrdinalPosition, rowData.ColumnName}

	row, err := spanner.NewRow(spannerCols, spannerVals)
	if err != nil {
		return nil, fmt.Errorf("mockRowIterator.Next: failed to create spanner.Row: %w", err)
	}
	return row, nil
}

func (it *mockRowIterator) Stop() {
	it.currentIndex = len(it.rows)
	it.closed = true
}

// mockReadOnlyTransaction implements ReadOnlyTransactionAPI
type mockReadOnlyTransaction struct {
	mockIterator *mockRowIterator
	queryError   error
	client       *mockSpannerClient
}

// Query implements the ReadOnlyTransactionAPI interface.
// It now returns RowIteratorAPI.
func (t *mockReadOnlyTransaction) Query(ctx context.Context, statement spanner.Statement) RowIteratorAPI {
	if t.client != nil {
		t.client.currentStmtSQL = statement.SQL
		t.client.currentStmtParams = statement.Params
	}
	if t.queryError != nil {
		return &mockRowIterator{err: t.queryError}
	}
	if t.mockIterator == nil {
		t.mockIterator = &mockRowIterator{rows: []mockRowData{}}
	}
	t.mockIterator.currentIndex = 0
	t.mockIterator.closed = false // Ensure iterator is not closed when Query is called
	return t.mockIterator
}

func (t *mockReadOnlyTransaction) Close() {}

// Implement other ReadOnlyTransactionAPI methods with panic
func (t *mockReadOnlyTransaction) AnalyzeQuery(ctx context.Context, statement spanner.Statement) (*spannerpb.QueryPlan, error) {
	panic("AnalyzeQuery not implemented in mock")
}

// Adjusted return types for other Query... methods to RowIteratorAPI
func (t *mockReadOnlyTransaction) QueryWithOptions(ctx context.Context, statement spanner.Statement, opts spanner.QueryOptions) RowIteratorAPI {
	panic("QueryWithOptions not implemented in mock")
}
func (t *mockReadOnlyTransaction) QueryWithStats(ctx context.Context, statement spanner.Statement) RowIteratorAPI {
	panic("QueryWithStats not implemented in mock")
}
func (t *mockReadOnlyTransaction) Read(ctx context.Context, table string, keys spanner.KeySet, columns []string) RowIteratorAPI {
	panic("Read not implemented in mock")
}
func (t *mockReadOnlyTransaction) ReadRow(ctx context.Context, table string, key spanner.Key, columns []string) (*spanner.Row, error) {
	panic("ReadRow not implemented in mock")
}
func (t *mockReadOnlyTransaction) ReadRowUsingIndex(ctx context.Context, table string, index string, key spanner.Key, columns []string) (*spanner.Row, error) {
	panic("ReadRowUsingIndex not implemented in mock")
}
func (t *mockReadOnlyTransaction) ReadRowWithOptions(ctx context.Context, table string, key spanner.Key, columns []string, opts *spanner.ReadOptions) (*spanner.Row, error) {
	panic("ReadRowWithOptions not implemented in mock")
}
func (t *mockReadOnlyTransaction) ReadUsingIndex(ctx context.Context, table, index string, keys spanner.KeySet, columns []string) RowIteratorAPI {
	panic("ReadUsingIndex not implemented in mock")
}
func (t *mockReadOnlyTransaction) ReadWithOptions(ctx context.Context, table string, keys spanner.KeySet, columns []string, opts *spanner.ReadOptions) RowIteratorAPI {
	panic("ReadWithOptions not implemented in mock")
}
func (t *mockReadOnlyTransaction) Timestamp() (time.Time, error) {
	panic("Timestamp not implemented in mock")
}
func (t *mockReadOnlyTransaction) WithTimestampBound(tb spanner.TimestampBound) ReadOnlyTransactionAPI {
	return t
}

// mockSpannerClient implements SpannerClientAPI
type mockSpannerClient struct {
	mockTxToReturn    *mockReadOnlyTransaction
	currentStmtSQL    string
	currentStmtParams map[string]interface{}
}

// Single implements SpannerClientAPI and now returns ReadOnlyTransactionAPI.
func (msc *mockSpannerClient) Single() ReadOnlyTransactionAPI {
	if msc.mockTxToReturn == nil {
		return &mockReadOnlyTransaction{mockIterator: &mockRowIterator{rows: []mockRowData{}}}
	}
	return msc.mockTxToReturn
}

func (msc *mockSpannerClient) Close()      {}
func (msc *mockSpannerClient) DatabaseName() string { return "mockdb" }


func TestSpanIndexColumns_Emulator(t *testing.T) {
	if err := os.Setenv("SPANNER_EMULATOR_HOST", "localhost:9010"); err != nil {
		t.Fatalf("Failed to set SPANNER_EMULATOR_HOST: %v", err)
	}
	defer os.Unsetenv("SPANNER_EMULATOR_HOST")

	mockRowsData := []mockRowData{
		{OrdinalPosition: spanner.NullInt64{Int64: 2, Valid: true}, ColumnName: "ColB"},
		{OrdinalPosition: spanner.NullInt64{Int64: 1, Valid: true}, ColumnName: "ColA"},
		{OrdinalPosition: spanner.NullInt64{Int64: 3, Valid: true}, ColumnName: "ColC"},
	}

	mockIter := &mockRowIterator{rows: mockRowsData}
	mockTx := &mockReadOnlyTransaction{mockIterator: mockIter}
	mockClient := &mockSpannerClient{mockTxToReturn: mockTx}
	mockTx.client = mockClient

	loader := NewSpannerLoader(mockClient)

	indexColumns, err := loader.IndexColumnList("TestTable", "TestIndex")

	if err != nil {
		t.Fatalf("SpanIndexColumns failed: %v", err)
	}

	expected := []*models.IndexColumn{
		{SeqNo: 1, ColumnName: "ColA"},
		{SeqNo: 2, ColumnName: "ColB"},
		{SeqNo: 3, ColumnName: "ColC"},
	}
	if !reflect.DeepEqual(indexColumns, expected) {
		t.Errorf("Emulator: Expected %+v, got %+v", expected, indexColumns)
	}

	expectedSQL := `SELECT ORDINAL_POSITION, COLUMN_NAME FROM INFORMATION_SCHEMA.INDEX_COLUMNS WHERE TABLE_SCHEMA = "" AND INDEX_NAME = @index AND TABLE_NAME = @table`
	if mockClient.currentStmtSQL != expectedSQL {
		t.Errorf("Emulator: SQL mismatch:\nExpected: %s\nGot:      %s", expectedSQL, mockClient.currentStmtSQL)
	}
}

func TestSpanIndexColumns_NoEmulator(t *testing.T) {
	os.Unsetenv("SPANNER_EMULATOR_HOST")

	mockRowsData := []mockRowData{
		{OrdinalPosition: spanner.NullInt64{Int64: 1, Valid: true}, ColumnName: "ColA"},
		{OrdinalPosition: spanner.NullInt64{Int64: 2, Valid: true}, ColumnName: "ColZ"},
		{OrdinalPosition: spanner.NullInt64{Int64: 3, Valid: true}, ColumnName: "ColM"},
	}

	mockIter := &mockRowIterator{rows: mockRowsData}
	mockTx := &mockReadOnlyTransaction{mockIterator: mockIter}
	mockClient := &mockSpannerClient{mockTxToReturn: mockTx}
	mockTx.client = mockClient

	loader := NewSpannerLoader(mockClient)

	indexColumns, err := loader.IndexColumnList("TestTable", "TestIndex")

	if err != nil {
		t.Fatalf("SpanIndexColumns failed: %v", err)
	}

	expected := []*models.IndexColumn{
		{SeqNo: 1, ColumnName: "ColA"},
		{SeqNo: 2, ColumnName: "ColZ"},
		{SeqNo: 3, ColumnName: "ColM"},
	}
	if !reflect.DeepEqual(indexColumns, expected) {
		t.Errorf("No Emulator: Expected %+v, got %+v", expected, indexColumns)
	}

	expectedSQL := `SELECT ORDINAL_POSITION, COLUMN_NAME FROM INFORMATION_SCHEMA.INDEX_COLUMNS WHERE TABLE_SCHEMA = "" AND INDEX_NAME = @index AND TABLE_NAME = @table ORDER BY ORDINAL_POSITION`
	if mockClient.currentStmtSQL != expectedSQL {
		t.Errorf("NoEmulator: SQL mismatch:\nExpected: %s\nGot:      %s", expectedSQL, mockClient.currentStmtSQL)
	}
}
