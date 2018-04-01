// Command debdiff implements a tool to view and manipulate a "system
// level diff" of sorts for apt/dpkg based systems. It's somewhat akin to the
// "things that differ" if a new system was given the exact current set of
// packages combined with a target directory that can be considered an
// "overlay" on top of the packages for things like configuration and or
// ignored data.
package main // import "github.com/daaku/debdiff"

import (
	"bufio"
	"crypto/md5"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"

	"github.com/gobwas/glob"
	"github.com/pkg/errors"
)

type Glob interface {
	Match(name string) bool
}

type simpleGlob string

func (g simpleGlob) Match(path string) bool {
	if path == string(g) {
		return true
	}
	return strings.HasPrefix(path, string(g)+"/")
}

func filehash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.Wrap(err, "filehash open error")
	}
	defer file.Close()
	h := md5.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", errors.Wrap(err, "filehash copy error")
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func contains(a []string, x string) bool {
	i := sort.SearchStrings(a, x)
	if i == len(a) {
		return false
	}
	return a[i] == x
}

type DebDiff struct {
	Silent     bool
	Root       string
	Repo       string
	IgnoreDir  string
	CpuProfile string

	ignoreGlob     []Glob
	allFile        []string
	pkgFile        []string
	repoFile       []string
	unpackagedFile []string
	diffRepoFile   []string
}

func (ad *DebDiff) buildIgnoreGlob() error {
	err := filepath.Walk(
		ad.IgnoreDir,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return errors.Wrap(err, "walking ignore directory")
			}
			if info.IsDir() {
				return nil
			}
			f, err := os.OpenFile(path, os.O_RDONLY, os.ModePerm)
			if err != nil {
				return errors.Wrap(err, "reading ignore file")
			}
			defer f.Close()

			sc := bufio.NewScanner(f)
			for sc.Scan() {
				l := sc.Text()
				if len(l) == 0 {
					continue
				}
				if l[0] == '#' {
					continue
				}
				if strings.IndexAny(l, "*?[") > -1 {
					g, err := glob.Compile(l)
					if err != nil {
						return errors.Wrap(err, "invalid glob pattern")
					}
					ad.ignoreGlob = append(ad.ignoreGlob, g)
				} else {
					ad.ignoreGlob = append(ad.ignoreGlob, simpleGlob(l))
				}
			}
			if err := sc.Err(); err != nil {
				return errors.Wrap(err, "reading ignore file")
			}
			return nil
		},
	)
	if err != nil {
		return errors.Wrap(err, "walking ignore directory")
	}
	return nil
}

func (ad *DebDiff) IsIgnored(path string) bool {
	for _, glob := range ad.ignoreGlob {
		if glob.Match(path) {
			return true
		}
	}
	return false
}

func (ad *DebDiff) buildAllFile() error {
	err := filepath.Walk(
		ad.Root,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsPermission(err) {
					if !ad.Silent {
						log.Printf("Skipping file: %s", err)
					}
					return nil
				}
				return errors.Wrap(err, "walking all files")
			}
			if ad.IsIgnored(path) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.IsDir() {
				return nil
			}
			ad.allFile = append(ad.allFile, path)
			return nil
		})
	if err != nil {
		return errors.Wrap(err, "walking all files")
	}
	sort.Strings(ad.allFile)
	return nil
}

func (ad *DebDiff) buildRepoFile() error {
	err := filepath.Walk(ad.Repo, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if !ad.Silent {
				log.Printf("RepoFile Walk error: %s", err)
			}
			return errors.Wrap(err, "walking repo files")
		}
		if info.IsDir() {
			return nil
		}
		name := strings.Replace(path, ad.Repo, "", 1)
		if name[0] != '/' {
			name = "/" + name
		}
		ad.repoFile = append(ad.repoFile, name)
		return nil
	})
	if err != nil {
		return errors.Wrap(err, "walking repo files")
	}
	sort.Strings(ad.repoFile)
	return nil
}

func (ad *DebDiff) buildPkgFile() error {
	lists, err := filepath.Glob(
		filepath.Join(ad.Root, "var/lib/dpkg/info") + "/*.list")
	if err != nil {
		return errors.Wrap(err, "looking for dpkg info lists")
	}
	conffiles, err := filepath.Glob(
		filepath.Join(ad.Root, "var/lib/dpkg/info") + "/*.conffiles")
	if err != nil {
		return errors.Wrap(err, "looking for dpkg info lists")
	}
	lists = append(lists, conffiles...)
	for _, list := range lists {
		f, err := os.OpenFile(list, os.O_RDONLY, os.ModePerm)
		if err != nil {
			return errors.Wrap(err, "reading dpkg info file")
		}
		defer f.Close()

		sc := bufio.NewScanner(f)
		for sc.Scan() {
			ad.pkgFile = append(ad.pkgFile, sc.Text())
		}
		if err := sc.Err(); err != nil {
			return errors.Wrap(err, "reading dpkg info file")
		}
	}
	sort.Strings(ad.pkgFile)
	return nil
}

func (ad *DebDiff) buildUnpackagedFile() error {
	for _, name := range ad.allFile {
		if contains(ad.repoFile, name) {
			continue
		}
		if contains(ad.pkgFile, name) {
			continue
		}
		ad.unpackagedFile = append(ad.unpackagedFile, name)
	}
	return nil
}

func (ad *DebDiff) buildDiffRepoFile() error {
	for _, file := range ad.repoFile {
		realpath := filepath.Join(ad.Root, file)
		repopath := filepath.Join(ad.Repo, file)
		realhash, err := filehash(realpath)
		if err != nil && !os.IsNotExist(errors.Cause(err)) {
			if os.IsPermission(errors.Cause(err)) {
				if !ad.Silent {
					log.Printf("Skipping file: %s", err)
				}
				continue
			}
			return err
		}
		repohash, err := filehash(repopath)
		if err != nil && !os.IsNotExist(err) {
			if os.IsPermission(err) {
				if !ad.Silent {
					log.Printf("Skipping file: %s", err)
				}
				continue
			}
			return err
		}
		if realhash != repohash {
			ad.diffRepoFile = append(ad.diffRepoFile, file)
		}
	}
	return nil
}

func Main() error {
	var ad DebDiff
	flag.BoolVar(&ad.Silent, "silent", false, "suppress errors")
	flag.StringVar(&ad.Root, "root", "/", "installation root")
	flag.StringVar(&ad.Repo, "repo", "/usr/share/debdiff", "repo directory")
	flag.StringVar(&ad.IgnoreDir, "ignore", "", "directory of ignore files")
	flag.StringVar(&ad.CpuProfile, "cpuprofile", "", "write cpu profile here")
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if ad.CpuProfile != "" {
		f, err := os.Create(ad.CpuProfile)
		if err != nil {
			return errors.Wrap(err, "error creating cpu profile")
		}
		defer f.Close()
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	steps := []func() error{
		ad.buildIgnoreGlob,
		ad.buildAllFile,
		ad.buildRepoFile,
		ad.buildPkgFile,
		ad.buildUnpackagedFile,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}

	for _, file := range ad.unpackagedFile {
		fmt.Println(file)
	}

	return nil
}

func main() {
	if err := Main(); err != nil {
		fmt.Fprintf(os.Stderr, "%+v", err)
		os.Exit(1)
	}
}
