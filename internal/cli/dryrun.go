package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/renv"
)

// runDryRunReport reports what running the DAG *would* do without executing
// any steps or creating a run directory. Reaches the same gating checks as
// the real runDAG path: secrets are already resolved by the caller; here we
// add R version, renv, freshness, and script-existence checks.
//
// Exit code 1 (returned as error) if any check is FAIL. Warnings do not fail.
func runDryRunReport(d *dag.DAG, dagPath string) error {
	fmt.Printf("Dry-run: %s (%s)\n", d.Name, dagPath)
	fmt.Println(strings.Repeat("=", 32))

	r := newDryRunReporter()

	r.ok("DAG YAML valid")
	r.ok("Secrets resolved")

	rVersion := detectRVersion()
	if d.RVersion != "" {
		if rVersion == "" {
			r.fail("R version requested >=%s but Rscript not found in PATH", d.RVersion)
		} else if msg, ok := dag.CheckRVersion(d.RVersion, rVersion); !ok {
			if d.RVersionStrict {
				r.fail("%s", msg)
			} else {
				r.warn("%s", msg)
			}
		} else {
			r.ok("R version %s satisfies %s", rVersion, d.RVersion)
		}
	} else if rVersion != "" {
		r.ok("R version %s detected", rVersion)
	}

	rPlatform := renv.DetectRPlatform()
	projectDir := resolveProjectDir(d)
	renvInfo := renv.Detect(projectDir, rVersion, rPlatform)
	switch {
	case !renvInfo.Detected:
		r.info("No renv.lock in project directory %s", projectDir)
	case renvInfo.LibraryReady:
		r.ok("renv library ready: %s", renvInfo.LibraryPath)
	default:
		r.warn("renv.lock found but library missing: %s — run renv::restore()", renvInfo.LibraryPath)
	}

	r.checkScripts(d)
	r.checkFreshness(d)

	fmt.Println()
	if err := r.printPlan(d); err != nil {
		return err
	}

	fmt.Println()
	if r.failed {
		return fmt.Errorf("dry-run reported failures")
	}
	return nil
}

type dryRunReporter struct {
	failed bool
}

func newDryRunReporter() *dryRunReporter { return &dryRunReporter{} }

func (r *dryRunReporter) ok(format string, args ...any) {
	fmt.Printf("[OK]   "+format+"\n", args...)
}
func (r *dryRunReporter) info(format string, args ...any) {
	fmt.Printf("[INFO] "+format+"\n", args...)
}
func (r *dryRunReporter) warn(format string, args ...any) {
	fmt.Printf("[WARN] "+format+"\n", args...)
}
func (r *dryRunReporter) fail(format string, args ...any) {
	fmt.Printf("[FAIL] "+format+"\n", args...)
	r.failed = true
}

func (r *dryRunReporter) checkScripts(d *dag.DAG) {
	var missing []string
	for _, s := range d.Steps {
		paths := scriptPathsFor(s)
		workdir := d.ResolveWorkdir(s)
		for _, p := range paths {
			if !filepath.IsAbs(p) {
				p = filepath.Join(workdir, p)
			}
			if _, err := os.Stat(p); err != nil {
				missing = append(missing, fmt.Sprintf("step %q: %s", s.ID, p))
			}
		}
	}
	if len(missing) > 0 {
		r.fail("Missing referenced scripts (%d):", len(missing))
		for _, m := range missing {
			fmt.Printf("       %s\n", m)
		}
		return
	}
	r.ok("All referenced scripts exist")
}

// scriptPathsFor returns the on-disk paths a step would need at execution.
// Only step types that name a file are included; inline r_expr/command have
// nothing to check.
func scriptPathsFor(s dag.Step) []string {
	var out []string
	add := func(p string) {
		if p != "" {
			out = append(out, p)
		}
	}
	add(s.Script)
	add(s.Quarto)
	add(s.Rmd)
	add(s.Validate)
	if s.Connect != nil {
		add(s.Connect.Path)
	}
	if s.Database != nil {
		add(s.Database.QueryFile)
	}
	return out
}

func (r *dryRunReporter) checkFreshness(d *dag.DAG) {
	var stale []string
	var fails []string
	for _, s := range d.Steps {
		workdir := d.ResolveWorkdir(s)
		for _, fc := range s.Freshness {
			p := fc.Path
			if !filepath.IsAbs(p) {
				p = filepath.Join(workdir, p)
			}
			info, err := os.Stat(p)
			if err != nil {
				fails = append(fails, fmt.Sprintf("step %q: %s: %v", s.ID, fc.Path, err))
				continue
			}
			maxAge, err := time.ParseDuration(fc.MaxAge)
			if err != nil {
				fails = append(fails, fmt.Sprintf("step %q: invalid max_age %q: %v", s.ID, fc.MaxAge, err))
				continue
			}
			age := time.Since(info.ModTime())
			if age > maxAge {
				msg := fmt.Sprintf("step %q source %s is %s old (max %s)", s.ID, fc.Path, age.Truncate(time.Second), maxAge)
				if fc.OnStale == "warn" {
					stale = append(stale, msg)
				} else {
					fails = append(fails, msg)
				}
			}
		}
	}
	if len(fails) > 0 {
		r.fail("Freshness violations (%d):", len(fails))
		for _, m := range fails {
			fmt.Printf("       %s\n", m)
		}
	}
	for _, m := range stale {
		r.warn("Freshness: %s", m)
	}
	if len(fails) == 0 && len(stale) == 0 {
		// Only print the OK line if any step actually has freshness checks.
		for _, s := range d.Steps {
			if len(s.Freshness) > 0 {
				r.ok("All freshness checks pass")
				return
			}
		}
	}
}

func (r *dryRunReporter) printPlan(d *dag.DAG) error {
	steps := dag.ExpandMatrix(d.Steps)
	tiers, err := dag.TopoSort(steps)
	if err != nil {
		r.fail("Topo sort: %v", err)
		return nil
	}
	fmt.Printf("Would execute %d steps in %d tier(s)", len(steps), len(tiers))
	if d.MaxParallel > 0 {
		fmt.Printf(" (max_parallel=%d)", d.MaxParallel)
	}
	fmt.Println(":")
	for i, tier := range tiers {
		ids := make([]string, len(tier))
		for j, s := range tier {
			ids[j] = s.ID
		}
		fmt.Printf("  tier %d: %s\n", i, strings.Join(ids, ", "))
	}
	return nil
}
