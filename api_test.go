package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	// Setup test environment
	ctx := context.Background()

	// Initialize test database connection
	dbConfig := &DBConfig{
		Host:              "localhost",
		Port:              5432,
		UserName:          "admin",
		Password:          "admin",
		DBName:            "expense_tracker",
		MaxConns:          5,
		MinConns:          1,
		MaxConnLifeTime:   15 * time.Minute,
		MaxConnIdleTime:   5 * time.Minute,
		HealthCheckPeriod: 1 * time.Minute,
	}

	db, err := NewPg(ctx, dbConfig)
	if err != nil {
		log.Fatalf("Failed to connect to test database: %v", err)
	}
	defer db.Close()

	// Create the expenses table if it doesn't exist
	_, err = db.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS expenses (
            id SERIAL PRIMARY KEY,
            description TEXT NOT NULL,
            amount DECIMAL(10,2) NOT NULL,
            category TEXT NOT NULL,
            date TIMESTAMP NOT NULL
        )
    `)
	if err != nil {
		log.Fatalf("Failed to create expenses table: %v", err)
	}

	// Clean up any test data
	_, err = db.Exec(ctx, "DELETE FROM expenses")
	if err != nil {
		log.Fatalf("Failed to clean test database: %v", err)
	}

	// Run tests
	exitCode := m.Run()
	os.Exit(exitCode)
}

func setupTestApp() (*App, *mux.Router) {
	ctx := context.Background()
	dbConfig := &DBConfig{
		Host:              "localhost",
		Port:              5432,
		UserName:          "admin",
		Password:          "admin",
		DBName:            "expense_tracker",
		MaxConns:          5,
		MinConns:          1,
		MaxConnLifeTime:   15 * time.Minute,
		MaxConnIdleTime:   5 * time.Minute,
		HealthCheckPeriod: 1 * time.Minute,
	}

	db, _ := NewPg(ctx, dbConfig)
	app := &App{DBClient: db}
	app.initDB(ctx)

	r := mux.NewRouter()
	r.HandleFunc("/api/expenses", app.getExpenses).Methods("GET")
	r.HandleFunc("/api/expenses", app.createExpense).Methods("POST")
	r.HandleFunc("/api/expenses/{id}", app.updateExpense).Methods("PUT")
	r.HandleFunc("/api/expenses/{id}", app.deleteExpense).Methods("DELETE")

	return app, r
}

func TestCreateExpense(t *testing.T) {
	app, router := setupTestApp()
	defer app.DBClient.Close()

	// Test data
	expense := Expense{
		Description: "Test Expense",
		Amount:      123.45,
		Category:    "Testing",
		Date:        time.Now().Round(time.Second),
	}
	expenseJSON, _ := json.Marshal(expense)

	// Create request
	req, _ := http.NewRequest("POST", "/api/expenses", bytes.NewBuffer(expenseJSON))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	// Serve request
	router.ServeHTTP(rr, req)

	// Check response
	assert.Equal(t, http.StatusCreated, rr.Code, "Should return 201 Created")

	// Verify response contains the created expense with ID
	var createdExpense Expense
	err := json.Unmarshal(rr.Body.Bytes(), &createdExpense)
	assert.NoError(t, err, "Should decode response JSON")
	assert.NotZero(t, createdExpense.ID, "Should return expense with ID")
	assert.Equal(t, expense.Description, createdExpense.Description)
	assert.Equal(t, expense.Amount, createdExpense.Amount)
	assert.Equal(t, expense.Category, createdExpense.Category)
	// Time comparison can be tricky due to JSON serialization, comparing strings
	assert.Equal(t, expense.Date.Format(time.RFC3339),
		createdExpense.Date.Format(time.RFC3339), "Dates should match")

	fmt.Printf("Created test expense with ID: %d\n", createdExpense.ID)
	return
}

func TestGetExpenses(t *testing.T) {
	app, router := setupTestApp()
	defer app.DBClient.Close()

	// Add test data
	testExpenses := []Expense{
		{
			Description: "Groceries",
			Amount:      67.89,
			Category:    "Food",
			Date:        time.Now().Add(-24 * time.Hour).Round(time.Second),
		},
		{
			Description: "Gas",
			Amount:      45.67,
			Category:    "Transportation",
			Date:        time.Now().Round(time.Second),
		},
	}

	ctx := context.Background()
	for _, exp := range testExpenses {
		_, err := app.DBClient.Exec(ctx,
			"INSERT INTO expenses (description, amount, category, date) VALUES ($1, $2, $3, $4)",
			exp.Description, exp.Amount, exp.Category, exp.Date)
		assert.NoError(t, err, "Should insert test expense")
	}

	// Create request
	req, _ := http.NewRequest("GET", "/api/expenses", nil)
	rr := httptest.NewRecorder()

	// Serve request
	router.ServeHTTP(rr, req)

	// Check response
	assert.Equal(t, http.StatusOK, rr.Code, "Should return 200 OK")

	// Verify response contains the expenses
	var expenses []Expense
	err := json.Unmarshal(rr.Body.Bytes(), &expenses)
	assert.NoError(t, err, "Should decode response JSON")
	assert.GreaterOrEqual(t, len(expenses), 2, "Should return at least 2 expenses")

	// Check that the items are ordered by date DESC
	if len(expenses) >= 2 {
		assert.True(t, expenses[0].Date.After(expenses[1].Date) ||
			expenses[0].Date.Equal(expenses[1].Date),
			"Expenses should be ordered by date DESC")
	}

	fmt.Printf("Retrieved %d expenses\n", len(expenses))
}

func TestUpdateExpense(t *testing.T) {
	app, router := setupTestApp()
	defer app.DBClient.Close()

	// Add test data
	testExpense := Expense{
		Description: "Initial Expense",
		Amount:      50.00,
		Category:    "Test",
		Date:        time.Now().Round(time.Second),
	}

	ctx := context.Background()
	var expenseID int
	err := app.DBClient.QueryRow(ctx,
		"INSERT INTO expenses (description, amount, category, date) VALUES ($1, $2, $3, $4) RETURNING id",
		testExpense.Description, testExpense.Amount, testExpense.Category, testExpense.Date).Scan(&expenseID)
	assert.NoError(t, err, "Should insert test expense")

	// Update data
	updatedExpense := Expense{
		ID:          expenseID,
		Description: "Updated Expense",
		Amount:      75.50,
		Category:    "Updated Category",
		Date:        time.Now().Add(1 * time.Hour).Round(time.Second),
	}
	expenseJSON, _ := json.Marshal(updatedExpense)

	// Create request
	req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/expenses/%d", expenseID), bytes.NewBuffer(expenseJSON))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	// Serve request
	router.ServeHTTP(rr, req)

	// Check response
	assert.Equal(t, http.StatusOK, rr.Code, "Should return 200 OK")

	// Verify the expense was updated in the database
	var updatedInDB Expense
	err = app.DBClient.QueryRow(ctx,
		"SELECT id, description, amount, category, date FROM expenses WHERE id = $1", expenseID).
		Scan(&updatedInDB.ID, &updatedInDB.Description, &updatedInDB.Amount, &updatedInDB.Category, &updatedInDB.Date)
	assert.NoError(t, err, "Should find the expense in DB")
	assert.Equal(t, updatedExpense.Description, updatedInDB.Description)
	assert.Equal(t, updatedExpense.Amount, updatedInDB.Amount)
	assert.Equal(t, updatedExpense.Category, updatedInDB.Category)
	assert.Equal(t, updatedExpense.Date.Format(time.RFC3339),
		updatedInDB.Date.Format(time.RFC3339), "Dates should match")

	fmt.Printf("Updated expense with ID: %d\n", expenseID)
}

func TestDeleteExpense(t *testing.T) {
	app, router := setupTestApp()
	defer app.DBClient.Close()

	// Add test data
	testExpense := Expense{
		Description: "Expense to Delete",
		Amount:      99.99,
		Category:    "Test",
		Date:        time.Now().Round(time.Second),
	}

	ctx := context.Background()
	var expenseID int
	err := app.DBClient.QueryRow(ctx,
		"INSERT INTO expenses (description, amount, category, date) VALUES ($1, $2, $3, $4) RETURNING id",
		testExpense.Description, testExpense.Amount, testExpense.Category, testExpense.Date).Scan(&expenseID)
	assert.NoError(t, err, "Should insert test expense")

	// Create request
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/expenses/%d", expenseID), nil)
	rr := httptest.NewRecorder()

	// Serve request
	router.ServeHTTP(rr, req)

	// Check response
	assert.Equal(t, http.StatusNoContent, rr.Code, "Should return 204 No Content")

	// Verify the expense was deleted from the database
	var count int
	err = app.DBClient.QueryRow(ctx,
		"SELECT COUNT(*) FROM expenses WHERE id = $1", expenseID).Scan(&count)
	assert.NoError(t, err, "Should query the DB")
	assert.Equal(t, 0, count, "Expense should be deleted from DB")

	fmt.Printf("Deleted expense with ID: %d\n", expenseID)
}

func TestExpenseNotFound(t *testing.T) {
	app, router := setupTestApp()
	defer app.DBClient.Close()

	// Non-existent ID
	nonExistentID := 9999

	// Test update for non-existent expense
	updatedExpense := Expense{
		Description: "Non-existent Expense",
		Amount:      100.00,
		Category:    "Test",
		Date:        time.Now(),
	}
	expenseJSON, _ := json.Marshal(updatedExpense)

	// Create update request
	updateReq, _ := http.NewRequest("PUT", fmt.Sprintf("/api/expenses/%d", nonExistentID), bytes.NewBuffer(expenseJSON))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRR := httptest.NewRecorder()

	// Serve update request
	router.ServeHTTP(updateRR, updateReq)

	// Check update response
	assert.Equal(t, http.StatusNotFound, updateRR.Code, "Should return 404 Not Found for update")

	// Create delete request
	deleteReq, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/expenses/%d", nonExistentID), nil)
	deleteRR := httptest.NewRecorder()

	// Serve delete request
	router.ServeHTTP(deleteRR, deleteReq)

	// Check delete response
	assert.Equal(t, http.StatusNotFound, deleteRR.Code, "Should return 404 Not Found for delete")

	fmt.Printf("Properly handled non-existent expense ID: %d\n", nonExistentID)
}

func TestInvalidInput(t *testing.T) {
	app, router := setupTestApp()
	defer app.DBClient.Close()

	// Invalid JSON
	invalidJSON := []byte(`{"description": "Invalid JSON", "amount": "not-a-number"}`)

	// Create request with invalid JSON
	req, _ := http.NewRequest("POST", "/api/expenses", bytes.NewBuffer(invalidJSON))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	// Serve request
	router.ServeHTTP(rr, req)

	// Check response
	assert.Equal(t, http.StatusBadRequest, rr.Code, "Should return 400 Bad Request for invalid JSON")

	fmt.Println("Properly handled invalid JSON input")
}
