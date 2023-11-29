package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/go-github/v56/github"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/oauth2"
)

// Config represents the structure of the config.json file.
type Config struct {
	GitHubToken string `json:"github_token"`
}

var (
	githubAPICalls = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "github_api_calls_total",
		Help: "Total number of GitHub API calls",
	})

	stackoverflowAPICalls = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stackoverflow_api_calls_total",
		Help: "Total number of StackOverflow API calls",
	})
)

func getGitHubIssues(repoOwner string, repo string) []GithubIssue {

	// OAuth setup
	ctx := context.Background()
	token := os.Getenv("GITHUB_TOKEN")

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	// Create client
	client := github.NewClient(tc)

	opt := &github.IssueListByRepoOptions{}
	var allIssues []*github.Issue

	for {
		issues, resp, err := client.Issues.ListByRepo(ctx, repoOwner, repo, opt)
		if err != nil {
			panic(err)
		}
		allIssues = append(allIssues, issues...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	// Extract issue details
	var issues []GithubIssue
	for _, issue := range allIssues {
		issues = append(issues, GithubIssue{
			Title:      issue.GetTitle(),
			Number:     issue.GetNumber(),
			Created_At: issue.GetCreatedAt().Time,
			Closed_At:  issue.GetClosedAt().Time,
			Repo:       repo,
		})
	}

	githubAPICalls.Inc()
	return issues
}

func getSOQuestions(technology string) []SOQuestion {

	tag := technology

	// Calculate the UNIX timestamp for the current time minus one hour
	oneHourAgo := time.Now().Add(-time.Hour).Unix()

	// Set up the API URL with the fromdate parameter
	apiURL := fmt.Sprintf("https://api.stackexchange.com/2.3/questions?order=desc&sort=activity&tagged=%s&site=stackoverflow&fromdate=%d", tag, oneHourAgo)

	//res, err := http.Get("https://api.stackexchange.com/questions?site=stackoverflow")
	res, err := http.Get(apiURL)
	if err != nil {
		panic(err)
	}

	var result map[string]interface{}
	json.NewDecoder(res.Body).Decode(&result)

	var questions []SOQuestion
	for _, item := range result["items"].([]interface{}) {
		q := item.(map[string]interface{})

		// Extract question details
		question := SOQuestion{}

		question.Technology = technology

		// Check if 'title' field is present and not nil
		if title, ok := q["title"]; ok && title != nil {
			question.Title = title.(string)
		} else {
			panic("Title is missing or nil")
		}

		// Check if 'body' field is present and not nil
		if body, ok := q["body"]; ok && body != nil {
			question.Body = body.(string)
		} else {
			question.Body = "No Body"
			//panic("Body is missing or nil")
		}

		// Check if 'closed_date' field is present and not nil
		if closedDate, ok := q["closed_date"]; ok && closedDate != nil {
			// Convert 'closed_date' to time.Time
			if closedTime, err := time.Unix(int64(closedDate.(float64)), 0).MarshalJSON(); err == nil {
				// Unmarshal 'closed_date' to time.Time
				if err := json.Unmarshal(closedTime, &question.Closed_At); err != nil {
					panic(err)
				}
			} else {
				panic(err)
			}
		}

		// Check if 'answers' field is present and not nil
		if answers, ok := q["answers"]; ok && answers != nil {
			// Extract answers
			var answerList []SOAnswer
			for _, ans := range answers.([]interface{}) {
				answer := SOAnswer{}
				// Check if 'body' field is present and not nil
				if body, ok := ans.(map[string]interface{})["body"]; ok && body != nil {
					answer.Body = body.(string)
				} else {
					answer.Body = "No answers yet"
					panic("Answer body is missing or nil")
				}
				answerList = append(answerList, answer)
			}
			question.Answers = answerList
		}

		questions = append(questions, question)
	}
	stackoverflowAPICalls.Inc()
	return questions
}

const (
	//user=postgres dbname=github password=root host=localhost sslmode=disable port = 5432
	githubDB = "user=postgres dbname=githubDB password=root host=/cloudsql/pg-go-docker-prometheus-gcp:us-central1:mypostgres sslmode=disable port=5432"
	soDB     = "user=postgres dbname=soDB password=root host=/cloudsql/pg-go-docker-prometheus-gcp:us-central1:mypostgres sslmode=disable port=5432"
	//githubDB = "user=postgres dbname=github password=root host=localhost sslmode=disable port=5432"
	//soDB     = "user=postgres dbname=so password=root host=localhost sslmode=disable port=5432"
)

type GithubIssue struct {
	Title      string `json:"title"`
	Number     int    `json:"number"`
	Created_At time.Time
	Closed_At  time.Time
	Repo       string
}

type SOQuestion struct {
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Answers    []SOAnswer `json:"answers"`
	Created_At time.Time
	Closed_At  time.Time
	Technology string
}

type SOAnswer struct {
	Body string `json:"body"`
}

func storeGithubIssue(issue GithubIssue, db *sql.DB) {

	sql := `INSERT INTO github_issues(title, issue_number, created_at, closed_at, repo) 
             VALUES ($1, $2, $3, $4, $5)`

	_, err := db.Exec(sql, issue.Title, issue.Number, issue.Created_At, issue.Closed_At, issue.Repo)
	if err != nil {
		panic(err)
	}
}

func storeSOQuestion(question SOQuestion, db *sql.DB) {

	fmt.Printf("inserting %s\n", question.Technology)
	sql := "INSERT INTO so_posts (title, body, created_at, closed_at, technology) VALUES ($1, $2, $3, $4, $5)"
	_, err := db.Exec(sql, question.Title, question.Body, question.Created_At, question.Closed_At, question.Technology)
	if err != nil {
		panic(err)
	}
}

func main() {

	// Read the contents of the config.json file
	file, err := ioutil.ReadFile("config.json")
	if err != nil {
		fmt.Println("Error reading config file:", err)
		return
	}

	// Parse the JSON content into the Config struct
	var config Config
	err = json.Unmarshal(file, &config)
	if err != nil {
		fmt.Println("Error unmarshalling JSON:", err)
		return
	}

	// Access the GitHub token
	githubToken := config.GitHubToken
	fmt.Println("GitHub Token:", githubToken)

	// Register metrics
	prometheus.MustRegister(githubAPICalls)
	prometheus.MustRegister(stackoverflowAPICalls)

	// Expose metrics endpoint
	//http.ListenAndServe(":2112", nil)
	//log.Fatal(http.ListenAndServe(fmt.Sprintf(":2112"), nil))
	//go func() {
	//	http.Handle("/metrics", promhttp.Handler())
	//	log.Fatal(http.ListenAndServe(":2112", nil))
	//}()

	db, err := sql.Open("cloudsqlpostgres", githubDB)
	if err != nil {
		log.Fatalf("Error on initializing database connection: %s", err.Error())
	}
	defer db.Close()

	conn, err := db.Conn(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	//Test the database connection
	log.Println("Testing database connection")
	err = db.Ping()
	if err != nil {
		log.Fatalf("Error on database connection: %s", err.Error())
	}
	log.Println("Database connection established")

	log.Println("Database query done!")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Start combined GitHub and StackOverflow server on port 8080
	go func() {
		log.Println("Starting combined server on port 8080...")

		// GitHub handler
		http.HandleFunc("/github", func(w http.ResponseWriter, r *http.Request) {
			// GitHub handler logic
			w.Write([]byte("GitHub functionality"))
		})

		// StackOverflow handler
		http.HandleFunc("/stackoverflow", func(w http.ResponseWriter, r *http.Request) {
			// StackOverflow handler logic
			w.Write([]byte("StackOverflow functionality"))
		})

		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
	}()

	drop_table := `drop table if exists github_issues`
	_, err = db.Exec(drop_table)
	if err != nil {
		panic(err)
	}

	create_table := `CREATE TABLE IF NOT EXISTS "github_issues" (
						id SERIAL PRIMARY KEY,
						title TEXT, 
						issue_number INT,
						created_at TIMESTAMPTZ, 
						closed_at TIMESTAMPTZ,
						repo TEXT);`

	_, _err := db.Exec(create_table)
	if _err != nil {
		panic(_err)
	}

	fmt.Println("Created Table for GitHub issues")

	repos := map[string]string{
		"golang":     "go",
		"prometheus": "prometheus",
		"SeleniumHQ": "selenium",
		"openai":     "openai-openapi",
		"docker":     "docker-py",
		"milvus-io":  "milvus",
	}
	for owner, repo := range repos {
		// Get GitHub issues for the current repository
		issues := getGitHubIssues(owner, repo)

		// Store each issue in the database
		for _, issue := range issues {
			storeGithubIssue(issue, db)
		}
	}

	//time.Sleep(2 * time.Minute)

	SOdb, err := sql.Open("cloudsqlpostgres", soDB)
	if err != nil {
		log.Fatalf("Error on initializing database connection: %s", err.Error())
	}
	defer SOdb.Close()

	//Test the database connection
	log.Println("Testing database connection")
	err = SOdb.Ping()
	if err != nil {
		log.Fatalf("Error on database connection: %s", err.Error())
	}
	log.Println("Database connection established")

	log.Println("Database query done!")

	SOport := os.Getenv("PORT")
	if SOport == "" {
		SOport = "8080"
	}
	//http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	//	w.Write([]byte("Hello, world!"))
	//})
	//go func() {
	//	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", SOport), nil))
	//}()

	drop_table = `drop table if exists so_posts`
	_, err = SOdb.Exec(drop_table)
	if err != nil {
		panic(err)
	}

	create_table_SO := `CREATE TABLE IF NOT EXISTS so_posts (
		id SERIAL PRIMARY KEY,
		title TEXT,
		body TEXT,
		created_at TIMESTAMPTZ,
		closed_at TIMESTAMPTZ,
		technology TEXT)`

	_, _err = SOdb.Exec(create_table_SO)
	if _err != nil {
		panic(_err)
	}

	fmt.Println("Created Table for StackOverflow Posts")

	technologies := [...]string{"Prometheus", "Selenium", "OpenAI", "Docker", "Milvus", "Go"}

	for i, tech := range technologies {
		fmt.Printf("Index: %d, Technology: %s\n", i, tech)

		// Get StackOverflow questions
		questions := getSOQuestions(tech)
		for _, q := range questions {
			storeSOQuestion(q, SOdb)
		}
	}

	SOdb.Close()
	conn.Close()
	db.Close()

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":2112"), nil))
}
