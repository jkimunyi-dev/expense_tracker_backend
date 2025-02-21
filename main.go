package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/cors"
)

type Expense struct {
	ID          int       `json:"id"`
	Description string    `json:"description"`
	Amount      float64   `json:"amount"`
	Category    string    `json:"category"`
	Date        time.Time `json:"date"`
}

type App struct {
	DBClient *pgxpool.Pool
}

type DBConfig struct {
	Host              string `mapstructure:"PG_HOST"`
	Port              int    `mapstructure:"PG_PORT"`
	UserName          string `mapstructure:"PG_USERNAME"`
	Password          string `mapstructure:"PG_PASSWORD"`
	DBName            string `mapstructure:"PG_DBNAME"`
	MaxConns          int32
	MinConns          int32
	MaxConnLifeTime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

var (
	pgOnce sync.Once
)

func main() {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbConfig := &DBConfig{
		Host:              "localhost",
		Port:              5432,
		UserName:          "admin",
		Password:          "admin",
		DBName:            "expense_tracker",
		MaxConns:          10,
		MinConns:          2,
		MaxConnLifeTime:   30 * time.Minute,
		MaxConnIdleTime:   10 * time.Minute,
		HealthCheckPeriod: 2 * time.Minute,
	}

	db, err := NewPg(rootCtx, dbConfig)
	if err != nil {
		slog.Error("Error connecting to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	app := &App{
		DBClient: db,
	}

	if err := app.initDB(rootCtx); err != nil {
		slog.Error("Error initializing database", "error", err)
		os.Exit(1)
	}

	r := mux.NewRouter()

	// Expense routes
	r.HandleFunc("/api/expenses", app.getExpenses).Methods("GET")
	r.HandleFunc("/api/expenses", app.createExpense).Methods("POST")
	r.HandleFunc("/api/expenses/{id}", app.updateExpense).Methods("PUT")
	r.HandleFunc("/api/expenses/{id}", app.deleteExpense).Methods("DELETE")

	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000", "http://54.226.1.246:3000"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type"},
		AllowCredentials: true,
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}

	slog.Info("Server starting", "port", port)
	log.Fatal(http.ListenAndServe("0.0.0.0:"+port, c.Handler(r)))
}

func NewPg(ctx context.Context, dbConfig *DBConfig) (*pgxpool.Pool, error) {
	connString := fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable",
		dbConfig.UserName, dbConfig.Password, dbConfig.Host, dbConfig.Port, dbConfig.DBName)

	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("error parsing pool config: %w", err)
	}

	config.MaxConns = dbConfig.MaxConns
	config.MinConns = dbConfig.MinConns
	config.MaxConnLifetime = dbConfig.MaxConnLifeTime
	config.MaxConnIdleTime = dbConfig.MaxConnIdleTime
	config.HealthCheckPeriod = dbConfig.HealthCheckPeriod

	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("unable to create connection pool: %w", err)
	}

	if err = db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("unable to ping database: %w", err)
	}

	slog.Info("Successfully connected to database")
	return db, nil
}

func (app *App) initDB(ctx context.Context) error {
	_, err := app.DBClient.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS expenses (
			id SERIAL PRIMARY KEY,
			description TEXT NOT NULL,
			amount DECIMAL(10,2) NOT NULL,
			category TEXT NOT NULL,
			date TIMESTAMP NOT NULL
		)
	`)
	return err
}

func (app *App) getExpenses(w http.ResponseWriter, r *http.Request) {
	rows, err := app.DBClient.Query(r.Context(),
		"SELECT id, description, amount, category, date FROM expenses ORDER BY date DESC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var expenses []Expense
	for rows.Next() {
		var e Expense
		err := rows.Scan(&e.ID, &e.Description, &e.Amount, &e.Category, &e.Date)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		expenses = append(expenses, e)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(expenses)
}

func (app *App) createExpense(w http.ResponseWriter, r *http.Request) {
	var expense Expense
	if err := json.NewDecoder(r.Body).Decode(&expense); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err := app.DBClient.QueryRow(r.Context(),
		"INSERT INTO expenses (description, amount, category, date) VALUES ($1, $2, $3, $4) RETURNING id",
		expense.Description, expense.Amount, expense.Category, expense.Date).Scan(&expense.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(expense)
}

func (app *App) updateExpense(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	var expense Expense
	if err := json.NewDecoder(r.Body).Decode(&expense); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_, err := app.DBClient.Exec(r.Context(),
		"UPDATE expenses SET description=$1, amount=$2, category=$3, date=$4 WHERE id=$5",
		expense.Description, expense.Amount, expense.Category, expense.Date, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(expense)
}

func (app *App) deleteExpense(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	_, err := app.DBClient.Exec(r.Context(), "DELETE FROM expenses WHERE id=$1", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
