// Copyright (C) 2015 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

var (
	nicknameRe = regexp.MustCompile(`\(([^\s]*)\)`)
	emailRe    = regexp.MustCompile(`<([^\s]*)>`)
)

type author struct {
	name     string
	nickname string
	emails   []string
	commits  int
	geekrank int
}

// The displayName is the name followed by nickname, if any
func (a author) displayName() string {
	s := a.name
	if a.hasNickName() {
		s = s + " (" + a.nickname + ")"
	}
	return s
}

// hasNickName returns true if there is a nick name and it's relevantly
// different from the actual name.
func (a author) hasNickName() bool {
	if a.nickname == "" {
		return false
	}
	if strings.EqualFold(strings.ReplaceAll(a.name, " ", ""), a.nickname) {
		return false
	}
	return true
}

func main() {
	authorsFile := flag.String("read-authors", "", "Name of canonical AUTHORS file")
	printAuthors := flag.Bool("authors", false, "Print the AUTHORS list")
	printNames := flag.Bool("names", false, "Print the name list")
	printStats := flag.Bool("stats", false, "Print the statistics")
	minContributions := flag.Int("min", 1, "Minimum number of contribution to show up in lists")
	geekrank := flag.Bool("geekrank", false, "Sort contributors by geekrank")
	excludeHashes := flag.String("exclude-commits", "", "File containing commit hashes to ignore")
	excludePattern := flag.String("exclude-pattern", "[bot]", "Skip names containing this string")
	flag.Parse()

	// Load exclude hashes, if any
	var exclude stringSet
	if *excludeHashes != "" {
		hashes := readAll(*excludeHashes)
		lines := strings.Split(string(hashes), "\n")
		exclude = stringSetFromStrings(lines)
	}

	// Load existing AUTHORS, if any
	var authors []author
	if *authorsFile != "" {
		authors = getAuthors(*authorsFile)
	}

	// Grab the set of thus known email addresses
	listed := make(stringSet)
	names := make(map[string]int)
	for i, a := range authors {
		names[a.name] = i
		for _, e := range a.emails {
			listed.add(e)
		}
	}

	// Grab the set of all known authors based on the git log, and add any
	// missing ones to the authors list.
	all := allAuthors(exclude)
	for email, name := range all {
		if listed.has(email) {
			continue
		}

		if _, ok := names[name]; ok && name != "" {
			// We found a match on name
			authors[names[name]].emails = append(authors[names[name]].emails, email)
			listed.add(email)
			continue
		}

		authors = append(authors, author{
			name:   name,
			emails: []string{email},
		})
		names[name] = len(authors) - 1
		listed.add(email)
	}

	// Count commits per author, for ranking
	getContributions(authors)

	// Filter on minimum contributions
	for i := 0; i < len(authors); i++ {
		if strings.Contains(authors[i].name, *excludePattern) || authors[i].commits < *minContributions {
			authors = append(authors[:i], authors[i+1:]...)
			i--
		}
	}

	// Sort by name and, optionally, rank
	sort.Sort(byName(authors))
	if *geekrank {
		sort.Sort(byGeekrank(authors))
	}

	if *printNames {
		var lines []string
		for _, author := range authors {
			lines = append(lines, author.displayName())
		}
		contributorNames := strings.Join(lines, ", ")
		fmt.Println(contributorNames)
	}

	if *printStats {
		for _, author := range authors {
			fmt.Printf("%5d %2d %s\n", author.commits, author.geekrank, author.displayName())
		}
	}

	if *printAuthors {
		for _, author := range authors {
			fmt.Printf("%s", author.displayName())
			for _, email := range author.emails {
				fmt.Printf(" <%s>", email)
			}
			fmt.Printf("\n")
		}
	}
}

func getAuthors(file string) []author {
	bs := readAll(file)
	lines := strings.Split(string(bs), "\n")
	var authors []author

	for _, line := range lines {
		if len(line) == 0 || line[0] == '#' {
			continue
		}

		fields := strings.Fields(line)
		var author author
		for _, field := range fields {
			if m := nicknameRe.FindStringSubmatch(field); len(m) > 1 {
				author.nickname = m[1]
			} else if m := emailRe.FindStringSubmatch(field); len(m) > 1 {
				author.emails = append(author.emails, m[1])
			} else {
				if author.name == "" {
					author.name = field
				} else {
					author.name = author.name + " " + field
				}
			}
		}

		authors = append(authors, author)
	}
	return authors
}

func readAll(path string) []byte {
	fd, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer fd.Close()

	bs, err := ioutil.ReadAll(fd)
	if err != nil {
		log.Fatal(err)
	}

	return bs
}

// Add number of commits per author to the author list.
func getContributions(authors []author) {
	buf := new(bytes.Buffer)
	cmd := exec.Command("git", "log", "--pretty=format:%ae")
	cmd.Stdout = buf
	err := cmd.Run()
	if err != nil {
		log.Fatal(err)
	}

	// email -> authors idx
	emailIdx := make(map[string]int)
	for i := range authors {
		for _, email := range authors[i].emails {
			emailIdx[email] = i
		}
	}

	for _, line := range strings.Split(buf.String(), "\n") {
		if idx, ok := emailIdx[line]; ok {
			authors[idx].commits++
		}
	}

	for i := range authors {
		// geekrank is just log2 of the number of commits
		authors[i].geekrank = int(math.Log2(float64(authors[i].commits)))
	}
}

// allAuthors returns the set of authors in the git commit log, except those
// in excluded commits.
func allAuthors(exclude stringSet) map[string]string {
	args := []string{"log", "--format=%H %ae %an"}
	cmd := exec.Command("git", args...)
	bs, err := cmd.Output()
	if err != nil {
		log.Fatal("git:", err)
	}

	names := make(map[string]string)
	for _, line := range bytes.Split(bs, []byte{'\n'}) {
		fields := strings.SplitN(string(line), " ", 3)
		if len(fields) != 3 {
			continue
		}
		hash, email, name := fields[0], fields[1], fields[2]

		if exclude.has(hash) {
			continue
		}

		if names[email] == "" {
			names[email] = name
		}
	}

	return names
}

type byGeekrank []author

func (l byGeekrank) Len() int { return len(l) }

func (l byGeekrank) Less(a, b int) bool {
	return l[a].geekrank > l[b].geekrank
}

func (l byGeekrank) Swap(a, b int) { l[a], l[b] = l[b], l[a] }

type byName []author

func (l byName) Len() int { return len(l) }

func (l byName) Less(a, b int) bool {
	aname := strings.ToLower(l[a].name)
	bname := strings.ToLower(l[b].name)
	return aname < bname
}

func (l byName) Swap(a, b int) { l[a], l[b] = l[b], l[a] }

// A simple string set type

type stringSet map[string]struct{}

func stringSetFromStrings(ss []string) stringSet {
	s := make(stringSet)
	for _, e := range ss {
		s.add(e)
	}
	return s
}

func (s stringSet) add(e string) {
	s[e] = struct{}{}
}

func (s stringSet) has(e string) bool {
	_, ok := s[e]
	return ok
}
