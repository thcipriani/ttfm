// ttfm
// ~~~~
//
// Time to first merge. Takes a path to a bunch of gerrit-managed git repos
// and a username and finds that user's first contribution.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	repos       string
	userPattern string
	debug       bool
)

type change struct {
	repo       string
	sha1       string
	datetime   time.Time
	author     string
	refs       string
	has_commit bool
}

func parseArgs() {
	reposUsage := "Path to git repositories"
	defaultRepos := "."
	flag.StringVar(&repos, "repos", defaultRepos, reposUsage)
	flag.StringVar(&repos, "r", defaultRepos, reposUsage+" (shorthand)")

	userUsage := "Pattern of users to search for"
	flag.StringVar(&userPattern, "users", "", userUsage)
	flag.StringVar(&userPattern, "u", "", userUsage+" (shorthand)")

	debugUsage := "Increase verbosity of output"
	flag.BoolVar(&debug, "verbose", false, debugUsage)
	flag.BoolVar(&debug, "v", false, debugUsage+" (shorthand)")

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

func gitGrep(repo string) change {
	out, err := exec.Command("git", "-C", repo, "log", "--glob=heads", "--format=%aN\t%H\t%at\t%D", fmt.Sprintf("--author=%s", userPattern), "--extended-regexp", "--regexp-ignore-case", "--reverse").CombinedOutput()
	oldest_change := change{repo: repo}
	if len(out) > 0 && err == nil {
		oldest_change.has_commit = true
		commits := strings.Split(string(out), "\n")
		oldest_commit := strings.Split(commits[0], "\t")
		oldest_change.author = oldest_commit[0]
		oldest_change.sha1 = oldest_commit[1]
		if len(strings.TrimSpace(oldest_commit[3])) > 0 {
			if strings.Contains(oldest_commit[3], ",") {
				branches := strings.Split(oldest_commit[3], ",")
				for _, branch := range branches {
					branch = strings.TrimSpace(branch)
					if strings.HasPrefix(branch, "refs/changes") {
						oldest_change.refs = branch
						break
					}
				}
			} else {
				oldest_change.refs = oldest_commit[3]
			}
		}
		timestamp, err := strconv.ParseInt(oldest_commit[2], 10, 64)
		if err != nil {
			log.Fatal(err)
		}
		oldest_change.datetime = time.Unix(timestamp, 0)
	}
	return oldest_change
}

func findOldest(changes []change) change {
	oldest := change{datetime: time.Now()}
	for _, cl := range changes {
		if cl.has_commit {
			if cl.datetime.Before(oldest.datetime) {
				oldest = cl
			}
		}
	}
	return oldest
}

// Find user's first commits across all repos
func findFirstCommit(gitDirs []string) change {
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU()*4)
	changes := []change{}
	for _, repo := range gitDirs {
		wg.Add(1)
		sem <- struct{}{}
		go func(repo string) {
			if debug {
				fmt.Printf("Checking '%s'\n", repo)
			}
			defer func() { <-sem }()
			defer wg.Done()
			changes = append(changes, gitGrep(repo))
		}(repo)
	}

	wg.Wait()
	return findOldest(changes)
}

func makeMetaBranch(changeRef string) string {
	return filepath.Join(filepath.Dir(changeRef), "meta")
}

func ttfr(cl change) {
	if len(cl.refs) > 0 {
		metaBranch := makeMetaBranch(cl.refs)
		out, err := exec.Command("git", "-C", cl.repo, "log", "--format=%at", "--grep=Status: merged", metaBranch).CombinedOutput()
		if err != nil {
			fmt.Println(cl)
			log.Fatalf("ERROR: The command '%s' failed with: %s", fmt.Sprintf("git -C %s log --format='%at' --grep='Status: merged' %s", cl.repo, metaBranch), err)
		}
		merge_time, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("git -C '%s' show %s by '%s' at %v (%v)\n", cl.repo, cl.sha1, cl.author, cl.datetime, time.Unix(merge_time, 0).Sub(cl.datetime))
	} else {
		fmt.Printf("git -C '%s' show %s by '%s' at %v\n", cl.repo, cl.sha1, cl.author, cl.datetime)
	}
}

func main() {
	parseArgs()
	gitDirs, err := findGitDirs()
	if err != nil {
		log.Fatal(err)
	}
	ttfr(findFirstCommit(gitDirs))
}
