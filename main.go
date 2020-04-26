package main

import (
	"context"
	"log"
	"flag"
	"fmt"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
        "sort"
	"strings"
	"strconv"
	"sync"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

type change struct {
	Repo           string    `json:"repo"`
	Sha1           string    `json:"sha1"`
	AuthorName     string    `json:"author_name"`
	AuthorEmail    string    `json:"author_email"`
	AuthorDate     time.Time `json:"author_date"`
	CommitterName  string    `json:"committer_name"`
	CommitterEmail string    `json:"committer_email"`
	CommitterDate  time.Time `json:"committer_date"`
	Refs           string    `json:"refs"`
	MergeTime      int64     `json:"merge_time"`
	Bug            string    `json:"bug"`
}

type changeDuration struct {
	change change
	duration time.Duration
}

var (
	repos string
	debug bool
	authors = map[string]change{}
	gitLogFormat = []string{
		"%at", // 0 authorTime
		"%aN", // 1 authorName
		"%aE", // 2 authorEmail
		"%H", //  3 commit_hash
		"%(trailers:key=Bug,separator=%x2C,valueonly=on)", // 4 bug
		"%D", // 5 refs
		"%ct", // 6 commiter time
		"%cE", // 7 commiter_email
		"%cN", // 8 committer_name
	}
)

func parseArgs() {
	reposUsage := "Path to git repositories"
	defaultRepos := "."
	flag.StringVar(&repos, "repos", defaultRepos, reposUsage)
	flag.StringVar(&repos, "r", defaultRepos, reposUsage + " (shorthand)")

	debugUsage := "Increase verbosity of output"
	flag.BoolVar(&debug, "verbose", false, debugUsage)
	flag.BoolVar(&debug, "v", false, debugUsage + " (shorthand)")

	flag.Parse()
}

func findGitDirs() ([]string, error) {
	gitDirs := []string{}
	err := filepath.Walk(repos, func(path string, f os.FileInfo, err error) error {
		if filepath.Ext(path) == ".git" {
			gitDirs = append(gitDirs, path)
		}
		return nil
	})
	return gitDirs, err
}

func repoName(repoPath string) string {
	name, err := filepath.Rel(repos, repoPath)
	if err != nil {
		log.Fatal(err)
	}
	return name
}

func gitLogFmt(repo string) string {
	null := "%x00"
	var out strings.Builder
	for _, v := range gitLogFormat {
		fmt.Fprintf(&out, "%s%s", v, null)
	}
	fmt.Fprintf(&out, "%s", repoName(repo))
	return out.String()
}

func gitLog(repo string) ([]string, error) {
	cmd := []string{
		"git",
		"--no-pager",
		"-C", repo,
		"log",
		"--glob=heads",
		fmt.Sprintf("--format=%s", gitLogFmt(repo)),
		"--author-date-order",
	}
	out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
	if (len(out) == 0) {
		return []string{}, nil
	}
	if err != nil {
		log.Fatalf("'%s' failed! %s", strings.Join(cmd, " "), err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n"), err
}

// Create a Time object from a timestamp
func timeFromUnix(unixEpoch string) time.Time {
	timestamp, err := strconv.ParseInt(strings.TrimSpace(unixEpoch), 10, 64)
	if err != nil {
		log.Fatal(err)
	}
	return time.Unix(timestamp, 0)
}

func refMetaFromGitLog(ref string) string {
	return filepath.Join(filepath.Dir(ref), "meta")
}

// Make a meta ref from a ref
func refFromGitLog(ref string) string {
	if (! strings.Contains(ref, "refs/changes")) {
		return ""
	}

	branches := strings.Split(ref, ",")
	for _, branch := range branches {
		branch = strings.TrimSpace(branch)
		if (strings.HasPrefix(branch, "refs/changes")) {
			return refMetaFromGitLog(branch)
		}
	}

	return strings.TrimSpace(branches[0])
}

func parseGitLog(commits []string) {
	for _, commit := range commits {
		if (commit == "") {
			continue
		}
		commit_parts := strings.Split(commit, "\x00")
		authorTime := timeFromUnix(commit_parts[0])
		commitTime := timeFromUnix(commit_parts[6])
		ref := refFromGitLog(commit_parts[5])
		authors[commit_parts[1]] = change{
			AuthorName: commit_parts[1],
			AuthorEmail: commit_parts[2],
			AuthorDate: authorTime,
			CommitterName: commit_parts[8],
			CommitterEmail: commit_parts[7],
			CommitterDate: commitTime,
			Sha1: commit_parts[3],
			Repo: commit_parts[9],
			Bug: commit_parts[4],
			Refs: ref,
		}
	}
}

func sortedGitLog(gitDirs []string) []string {
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU() * 4)
	changes := []string{}
	for _, repo := range gitDirs {
		wg.Add(1)
		sem <- struct{}{}
		go func(repo string) {
			if (debug) {
				fmt.Printf("Checking '%s'\n", repo)
			}
			defer func() { <-sem }()
			defer wg.Done()
			out, err := gitLog(repo)
			if err != nil {
				log.Fatalf("Repo '%s' failed with: %s", repo, err)
			}
			changes = append(changes, out...)
		}(repo)
	}

	wg.Wait()
	sort.Sort(sort.Reverse(sort.StringSlice(changes)))
	return changes
}

func getMergeTime(cl change) time.Time {
        cmd := []string{
		"git",
		"-C",
		filepath.Join(repos, cl.Repo),
		"log",
		"--format=%at",
		"--grep=Status: merged",
		cl.Refs,
	}
	out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
	if err != nil {
		fmt.Println(cl)
		log.Fatalf(
			"ERROR: The command '%s' failed with: %s",
			strings.Join(cmd, " "),
			err)
	}
	if string(out) == "" {
		log.Fatalf("ERROR: The command '%s' failed", strings.Join(cmd, " "))
	}
	return timeFromUnix(string(out))
}

func main() {
	parseArgs()
	gitDirs, err := findGitDirs()
	if err != nil {
		log.Fatal(err)
	}
	parseGitLog(sortedGitLog(gitDirs))
	es, err := elasticsearch.NewDefaultClient()
	if err != nil {
		log.Fatalf("Error creating the client: %s", err)
	}
        i := 0
	for _, commit := range authors {
		// Only show commits from the past 3 months
		// if commit.AuthorDate.Before(time.Now().AddDate(0, -3, 0)) {
		// 	continue
		// }
		if commit.Refs == "" {
			continue
		}
		commit.MergeTime = getMergeTime(commit).Sub(commit.AuthorDate).Milliseconds()
		body, err := json.Marshal(commit)
		if err != nil {
			log.Fatal(err)
		}
		// Set up the request object.
		req := esapi.IndexRequest{
			Index:      "ttfm",
			DocumentID: strconv.Itoa(i + 1),
			Body:       strings.NewReader(string(body)),
			Refresh:    "true",
	        }

		// Perform the request with the client.
		res, err := req.Do(context.Background(), es)
		if err != nil {
			log.Fatalf("Error getting response: %s", err)
		}
		defer res.Body.Close()
		if res.IsError() {
			log.Printf("[%s] Error indexing document ID=%d", res.Status(), i+1)
		} else {
			fmt.Println(string(body))
		}
		i++
	}
}
