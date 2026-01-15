package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// Config
const (
	UploadDir   = "./uploads"
	GeminiKey   = "AIzaSyDd5vB_CXtHjEWA4mswVXwZ3Qqpg03RNf0" // Hardcoded as requested
	GeminiModel = "gemini-1.5-flash"                        // Using latest flash model
)

// Report models the waste report
type Report struct {
	ID            int     `json:"id"`
	ReporterName  string  `json:"reporter_name"`
	Whatsapp      string  `json:"whatsapp"`
	WasteCategory string  `json:"waste_category"`
	WasteDetail   string  `json:"waste_detail"`
	ImagePath     string  `json:"image_path"`
	Latitude      float64 `json:"latitude"`
	Longitude     float64 `json:"longitude"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
}

var db *sql.DB

func main() {
	// 1. Ensure Upload Directory Exists
	if _, err := os.Stat(UploadDir); os.IsNotExist(err) {
		os.Mkdir(UploadDir, 0755)
	}

	// 2. Connect to MySQL
	var err error
	db, err = sql.Open("mysql", "root:@tcp(127.0.0.1:3306)/go_crud")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	fmt.Println("Connected to MySQL!")

	// 3. Define Routes
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/login", loginPageHandler) // Admin Login Page

	// API Routes
	http.HandleFunc("/api/analyze", analyzeHandler)
	http.HandleFunc("/api/report", submitReportHandler)
	http.HandleFunc("/api/login", loginAPIHandler)
	http.HandleFunc("/api/logout", logoutAPIHandler) // Logout Handler

	// User Routes
	http.HandleFunc("/api/user/reports", userReportsHandler) // GET: Get reports by WA number

	// Admin Routes (Protected)
	http.HandleFunc("/admin", adminMiddleware(adminPageHandler))
	http.HandleFunc("/admin/detail", adminMiddleware(detailPageHandler))
	http.HandleFunc("/api/reports", adminMiddleware(reportsListHandler))
	http.HandleFunc("/api/status", adminMiddleware(updateStatusHandler))
	http.HandleFunc("/api/reporthandler", adminMiddleware(getSingleReportHandler))

	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(UploadDir))))

	fmt.Println("Server running at http://localhost:8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

// Middleware to check for cookie AND prevent caching
func adminMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Prevent Caching
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		cookie, err := r.Cookie("session_token")
		if err != nil || cookie.Value != "valid-admin-session" {
			if len(r.URL.Path) > 4 && r.URL.Path[:4] == "/api" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return
		}
		next(w, r)
	}
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.ServeFile(w, r, "report.html")
		return
	} else if r.URL.Path == "/manifest.json" {
		w.Header().Set("Content-Type", "application/manifest+json")
		http.ServeFile(w, r, "manifest.json")
		return
	} else if r.URL.Path == "/sw.js" {
		w.Header().Set("Content-Type", "application/javascript")
		http.ServeFile(w, r, "sw.js")
		return
	} else if r.URL.Path == "/icon.svg" {
		w.Header().Set("Content-Type", "image/svg+xml")
		http.ServeFile(w, r, "icon.svg")
		return
	} else if r.URL.Path == "/logo.png" {
		w.Header().Set("Content-Type", "image/png")
		http.ServeFile(w, r, "logo.png")
		return
	}
	http.NotFound(w, r)
}

func loginPageHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "login.html")
}

func adminPageHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "admin.html")
}

func detailPageHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "detail.html")
}

// loginAPIHandler creates a session cookie
func loginAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	user := r.FormValue("username")
	pass := r.FormValue("password")

	if user == "admin" && pass == "admin123" {
		http.SetCookie(w, &http.Cookie{
			Name:     "session_token",
			Value:    "valid-admin-session",
			Path:     "/",
			HttpOnly: true,
		})
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	} else {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
	}
}

// updateStatusHandler updates report status (e.g. "Selesai")
func updateStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	id := r.FormValue("id")
	status := r.FormValue("status")

	_, err := db.Exec("UPDATE reports SET status = ? WHERE id = ?", status, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// getSingleReportHandler fetches one report by ID
func getSingleReportHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	row := db.QueryRow("SELECT id, reporter_name, whatsapp, waste_category, waste_detail, image_path, latitude, longitude, status, created_at FROM reports WHERE id = ?", id)

	var rep Report
	var createdAtBytes []byte
	if err := row.Scan(&rep.ID, &rep.ReporterName, &rep.Whatsapp, &rep.WasteCategory, &rep.WasteDetail, &rep.ImagePath, &rep.Latitude, &rep.Longitude, &rep.Status, &createdAtBytes); err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	rep.CreatedAt = string(createdAtBytes)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rep)
}

// logoutAPIHandler clears the session cookie
func logoutAPIHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// userReportsHandler fetches reports for a specific WA number
func userReportsHandler(w http.ResponseWriter, r *http.Request) {
	wa := r.URL.Query().Get("whatsapp")
	if wa == "" {
		http.Error(w, "Whatsapp number required", http.StatusBadRequest)
		return
	}

	rows, err := db.Query("SELECT id, waste_category, waste_detail, status, created_at FROM reports WHERE whatsapp = ? ORDER BY id DESC", wa)
	if err != nil {
		log.Println("DB Error:", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	reports := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var cat, det, stat string
		var created []byte
		if err := rows.Scan(&id, &cat, &det, &stat, &created); err != nil {
			continue
		}
		reports = append(reports, map[string]interface{}{
			"id": id, "category": cat, "detail": det, "status": stat, "created_at": string(created),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reports)
}

// analyzeHandler: Uploads image -> Returns AI Result
func analyzeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.ParseMultipartForm(10 << 20)

	file, handler, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "Error retrieving image", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Save temp file for analysis (could be optimized to stream, but file is easier for logic reuse)
	filename := fmt.Sprintf("temp_%d_%s", time.Now().Unix(), handler.Filename)
	filePath := filepath.Join(UploadDir, filename)

	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Error saving temp file", http.StatusInternalServerError)
		return
	}
	io.Copy(dst, file)
	dst.Close()

	// Analyze
	category, detail, err := classifyWaste(filePath, handler.Header.Get("Content-Type"))

	// Delete temp file after analysis to save space
	os.Remove(filePath)

	if err != nil {
		log.Println("AI Error:", err)
		http.Error(w, "AI Analysis Failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":   "success",
		"category": category,
		"detail":   detail,
	})
}

// submitReportHandler: Saves the report with User info + Image + AI Result (sent from client)
func submitReportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.ParseMultipartForm(10 << 20)

	name := r.FormValue("name")
	wa := r.FormValue("whatsapp")
	latStr := r.FormValue("latitude")
	longStr := r.FormValue("longitude")
	category := r.FormValue("category") // Trust client or re-analyze (Trusting for speed)
	detail := r.FormValue("detail")

	file, handler, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "Error retrieving image", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Save permanent file
	filename := fmt.Sprintf("%d_%s", time.Now().Unix(), handler.Filename)
	filePath := filepath.Join(UploadDir, filename)
	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Error saving file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	io.Copy(dst, file)

	// Save to DB
	_, err = db.Exec("INSERT INTO reports (reporter_name, whatsapp, waste_category, waste_detail, image_path, latitude, longitude) VALUES (?, ?, ?, ?, ?, ?, ?)",
		name, wa, category, detail, "/uploads/"+filename, latStr, longStr)

	if err != nil {
		log.Println("DB Error:", err)
		http.Error(w, "Error saving to database", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// reportsListHandler returns all reports as JSON
func reportsListHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, reporter_name, whatsapp, waste_category, waste_detail, image_path, latitude, longitude, status, created_at FROM reports ORDER BY id DESC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	reports := []Report{}
	for rows.Next() {
		var rep Report
		var createdAtBytes []byte
		// MySQL DATETIME can be scanned into []byte or string.
		if err := rows.Scan(&rep.ID, &rep.ReporterName, &rep.Whatsapp, &rep.WasteCategory, &rep.WasteDetail, &rep.ImagePath, &rep.Latitude, &rep.Longitude, &rep.Status, &createdAtBytes); err != nil {
			log.Println("Scan Error:", err)
			continue
		}
		rep.CreatedAt = string(createdAtBytes)
		reports = append(reports, rep)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reports)
}

// classifyWaste uses Gemini AI to analyze the image
func classifyWaste(imagePath string, mimeType string) (string, string, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(GeminiKey))
	if err != nil {
		return "", "", fmt.Errorf("failed to create client: %v", err)
	}
	defer client.Close()

	// Use user provided model name
	model := client.GenerativeModel("gemini-flash-latest")

	// Read image file
	imgData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read image: %v", err)
	}

	// Determine format (jpeg, png, etc.)
	ext := filepath.Ext(imagePath)
	if len(ext) > 1 {
		ext = ext[1:] // remove dot
	}
	if ext == "jpg" {
		ext = "jpeg"
	}

	prompt := []genai.Part{
		genai.ImageData(ext, imgData),
		genai.Text(`Analyze this image of waste.
		1. Classify it into exactly one of these categories: "Organik", "Anorganik", or "B3" (Hazardous).
		2. Provide a short specific name/description of the waste (e.g., "Botol Plastik", "Kulit Pisang").
		Return the response in this specific JSON format: {"category": "...", "detail": "..."}.
		Do not include markdown code blocks. Just raw JSON.`),
	}

	resp, err := model.GenerateContent(ctx, prompt...)
	if err != nil {
		return "", "", fmt.Errorf("AI generation failed: %v", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", "", fmt.Errorf("no response from AI")
	}

	txt, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", "", fmt.Errorf("unexpected response format")
	}

	jsonStr := string(txt)
	log.Printf("Raw AI Response: %s", jsonStr) // Log for debugging

	// Strip Markdown Code Blocks if present (```json ... ```)
	if len(jsonStr) > 7 && jsonStr[:3] == "```" {
		// Find start and end of clean JSON
		start := 0
		for i, c := range jsonStr {
			if c == '{' {
				start = i
				break
			}
		}
		end := len(jsonStr)
		for i := len(jsonStr) - 1; i >= 0; i-- {
			if jsonStr[i] == '}' {
				end = i + 1
				break
			}
		}
		if start < end {
			jsonStr = jsonStr[start:end]
		}
	}

	var result struct {
		Category string `json:"category"`
		Detail   string `json:"detail"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		log.Printf("JSON Unmarshal Error: %v. String: %s", err, jsonStr)
		return "AI Parsing Error", jsonStr, nil
	}

	return result.Category, result.Detail, nil
}
