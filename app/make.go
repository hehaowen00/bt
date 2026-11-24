package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"bt/metainfo"
)

type eventMakeProgress struct {
	tcell.EventTime
	name    string
	percent int
}

type eventMade struct {
	tcell.EventTime
	meta *metainfo.Torrent
	out  string
	dir  string
	err  error
}

// askMake asks for the files, the trackers, whether the torrent is private
// and where to save it, then makes it in the background.
func (a *App) askMake() {
	if a.making {
		a.cmd.error(errMaking)
		return
	}

	a.cmd.ask(&prompt{
		label:    promptMakeFrom,
		complete: completeAny,
		onSubmit: func(src string) {
			if src == "" {
				return
			}

			path, err := filepath.Abs(expandHome(src))
			if err == nil {
				_, err = os.Stat(path)
			}

			if err != nil {
				a.cmd.error("%v", err)
				return
			}

			a.askTrackers(metainfo.CreateOptions{
				Path: path,
			})
		},
	})
}

func (a *App) askTrackers(opts metainfo.CreateOptions) {
	a.cmd.ask(&prompt{
		label: promptTrackers,
		onSubmit: func(text string) {
			trackers, err := parseTrackers(text)
			if err != nil {
				a.cmd.error("%v", err)
				return
			}

			opts.Trackers = trackers

			if len(trackers) == 0 {
				a.askSaveAs(opts)
				return
			}

			a.cmd.ask(&prompt{
				label:   promptPrivate + " ",
				confirm: true,
				onSubmit: func(string) {
					opts.Private = true
					a.askSaveAs(opts)
				},
				onCancel: func() {
					a.askSaveAs(opts)
				},
			})
		},
	})
}

// parseTrackers reads URLs separated by spaces, each its own tier.
func parseTrackers(text string) ([][]string, error) {
	var tiers [][]string

	for _, f := range strings.Fields(text) {
		u, err := url.Parse(f)
		if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "udp" {
			return nil, fmt.Errorf(errTracker, f)
		}

		tiers = append(tiers, []string{f})
	}

	return tiers, nil
}

func (a *App) askSaveAs(opts metainfo.CreateOptions) {
	p := &prompt{
		label:    promptSaveAs,
		complete: completePath,
		onSubmit: func(out string) {
			if out == "" {
				return
			}

			out = expandHome(out)

			_, err := os.Stat(out)
			if err == nil {
				a.cmd.confirm(sprintf(promptReplace, filepath.Base(out)), func() {
					a.make(opts, out)
				})

				return
			}

			a.make(opts, out)
		},
	}
	p.edit.set(opts.Path + ".torrent")
	a.cmd.ask(p)
}

func (a *App) make(opts metainfo.CreateOptions, out string) {
	a.making = true
	name := filepath.Base(opts.Path)
	a.cmd.info(msgHashing, name, 0)

	go func() {
		last := 0
		m, err := metainfo.Create(context.Background(), opts, func(done, total int) {
			pct := done * 100 / total
			if pct == last {
				return
			}

			last = pct
			ev := &eventMakeProgress{
				name:    name,
				percent: pct,
			}
			ev.SetEventNow()
			_ = a.s.PostEvent(ev)
		})

		if err == nil {
			err = os.WriteFile(out, m.Encode(), 0o644)
		}

		ev := &eventMade{
			meta: m,
			out:  out,
			dir:  filepath.Dir(opts.Path),
			err:  err,
		}
		ev.SetEventNow()
		_ = a.s.PostEvent(ev)
	}()
}

func (a *App) made(ev *eventMade) {
	a.making = false

	if ev.err != nil {
		a.cmd.error("%v", ev.err)
		return
	}

	_, err := a.sess.Seed(ev.meta, ev.dir)
	if err != nil {
		a.cmd.error("%v", err)
		return
	}

	a.cmd.info(msgMade, filepath.Base(ev.out))
}

func expandHome(p string) string {
	rest, ok := strings.CutPrefix(p, "~")
	if !ok || rest != "" && rest[0] != '/' {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}

	return filepath.Join(home, rest)
}
