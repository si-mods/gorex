package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/iconpack"
	"gorex/locales"
)

var iconPacksUI struct {
	mu              sync.Mutex
	installed       []iconpack.Summary
	installedLoaded bool
	reg             *iconpack.RegistryIndex
	regLoaded       bool
	regLoading      bool
	regErr          string
	query           string
	installing      map[string]bool
	actionErr       string
}

func resetIconPacksUI() {
	iconPacksUI.mu.Lock()
	defer iconPacksUI.mu.Unlock()
	iconPacksUI.installed = nil
	iconPacksUI.installedLoaded = false
	iconPacksUI.reg = nil
	iconPacksUI.regLoaded = false
	iconPacksUI.regLoading = false
	iconPacksUI.regErr = ""
	iconPacksUI.query = ""
	iconPacksUI.installing = nil
	iconPacksUI.actionErr = ""
}

func (a *App) refreshInstalledIconPacks() {
	list, err := iconpack.ListPacks(iconpack.Dir())
	iconPacksUI.mu.Lock()
	iconPacksUI.installed = list
	iconPacksUI.installedLoaded = true
	if err != nil {
		iconPacksUI.actionErr = err.Error()
	}
	iconPacksUI.mu.Unlock()
	reloadIconPacks(a.luaConfig)
}

func (a *App) fetchIconPackRegistry() {
	iconPacksUI.mu.Lock()
	if iconPacksUI.regLoading {
		iconPacksUI.mu.Unlock()
		return
	}
	iconPacksUI.regLoading = true
	iconPacksUI.regErr = ""
	iconPacksUI.mu.Unlock()

	win := settingsUI.win
	go func() {
		idx, err := iconpack.FetchRegistry(context.Background(), "")
		apply := func() {
			iconPacksUI.mu.Lock()
			iconPacksUI.regLoading = false
			iconPacksUI.regLoaded = true
			if err != nil {
				iconPacksUI.regErr = err.Error()
			} else {
				iconPacksUI.reg = idx
				iconPacksUI.regErr = ""
			}
			iconPacksUI.mu.Unlock()
		}
		if win != nil {
			win.Update(apply)
		} else {
			apply()
		}
	}()
}

func (a *App) installIconPack(rp iconpack.RegistryPack) {
	iconPacksUI.mu.Lock()
	if iconPacksUI.installing == nil {
		iconPacksUI.installing = map[string]bool{}
	}
	if iconPacksUI.installing[rp.ID] {
		iconPacksUI.mu.Unlock()
		return
	}
	iconPacksUI.installing[rp.ID] = true
	iconPacksUI.actionErr = ""
	iconPacksUI.mu.Unlock()

	win := settingsUI.win
	packsDir := iconpack.Dir()
	go func() {
		_, err := iconpack.InstallFromRegistry(context.Background(), rp, packsDir, "")
		apply := func() {
			iconPacksUI.mu.Lock()
			delete(iconPacksUI.installing, rp.ID)
			if err != nil {
				iconPacksUI.actionErr = err.Error()
			}
			iconPacksUI.mu.Unlock()
			if err == nil {
				a.setPref(func(s *settings) {
					if !slices.Contains(s.IconPacks, rp.ID) {
						s.IconPacks = append(s.IconPacks, rp.ID)
					}
				})
				a.refreshInstalledIconPacks()
			}
		}
		if win != nil {
			win.Update(apply)
		} else {
			apply()
		}
	}()
}

func (a *App) iconPacksSettings(c *ui.Context, k *colors) {
	iconPacksUI.mu.Lock()
	installedLoaded := iconPacksUI.installedLoaded
	regLoaded := iconPacksUI.regLoaded
	regLoading := iconPacksUI.regLoading
	installed := slices.Clone(iconPacksUI.installed)
	reg := iconPacksUI.reg
	regErr := iconPacksUI.regErr
	actionErr := iconPacksUI.actionErr
	installing := make(map[string]bool, len(iconPacksUI.installing))
	for id, v := range iconPacksUI.installing {
		installing[id] = v
	}
	iconPacksUI.mu.Unlock()

	if !installedLoaded {
		a.refreshInstalledIconPacks()
		iconPacksUI.mu.Lock()
		installed = slices.Clone(iconPacksUI.installed)
		iconPacksUI.mu.Unlock()
	}
	if !regLoaded && !regLoading {
		a.fetchIconPackRegistry()
	}

	prio := 0
	if prefs.IconPackPriority == "osc" {
		prio = 1
	}
	settingsGroup(c, k, "", "", "", func() {
		settingRow(c, k, locales.SettingsIconPackPriority(), "", func() {
			if ui.Segmented(c, &prio, locales.SettingsIconPackPriorityPack(), locales.SettingsIconPackPriorityOSC()).Label(locales.SettingsIconPackPriority()).Changed() {
				next := "pack"
				if prio == 1 {
					next = "osc"
				}
				a.setPref(func(s *settings) { s.IconPackPriority = next })
				reloadIconPacks(a.luaConfig)
			}
		})
	})

	settingsGroup(c, k, locales.SettingsIconPacksInstalled(), "", actionErr, func() {
		if len(installed) == 0 {
			groupRow(c, k, "installed-empty").Children(func() {
				rowHint(c, k, locales.SettingsIconPacksEmpty())
			})
			return
		}
		for _, s := range installed {
			groupRow(c, k, "installed-"+s.ID).Children(func() {
				title := cmp.Or(s.Name, s.ID)
				rowLine(c, k, title, func() {
					enabled := slices.Contains(prefs.IconPacks, s.ID)
					if ui.Switch(c, &enabled).Label(title).Changed() {
						a.setPref(func(st *settings) {
							if enabled {
								if !slices.Contains(st.IconPacks, s.ID) {
									st.IconPacks = append(st.IconPacks, s.ID)
								}
							} else {
								st.IconPacks = slices.DeleteFunc(st.IconPacks, func(x string) bool { return x == s.ID })
							}
						})
						reloadIconPacks(a.luaConfig)
					}
					if ui.Button(c, locales.SettingsIconPacksRemove()).Clicked() {
						if err := iconpack.Remove(iconpack.Dir(), s.ID); err != nil {
							iconPacksUI.mu.Lock()
							iconPacksUI.actionErr = err.Error()
							iconPacksUI.mu.Unlock()
						} else {
							a.setPref(func(st *settings) {
								st.IconPacks = slices.DeleteFunc(st.IconPacks, func(x string) bool { return x == s.ID })
							})
							a.refreshInstalledIconPacks()
						}
					}
				})
				sub := s.ID
				if s.Version != "" {
					sub += " · " + s.Version
				}
				if s.Error != "" {
					sub += " · " + s.Error
				} else {
					sub += fmt.Sprintf(" · %d icons", s.Entries)
				}
				rowHint(c, k, sub)
			})
		}
	})

	installedSet := make(map[string]bool, len(installed))
	for _, s := range installed {
		installedSet[s.ID] = true
	}

	settingsGroup(c, k, locales.SettingsIconPacksRegistry(), "", regErr, func() {
		groupRow(c, k, "registry-toolbar").Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &iconPacksUI.query).Placeholder(locales.SettingsIconPacksSearch()).Label(locales.SettingsIconPacksSearch()).Grow(1).MinWidth(0)
				if ui.Button(c, locales.SettingsIconPacksRefresh()).Clicked() {
					a.fetchIconPackRegistry()
				}
			})
		})
		matches := iconpack.SearchRegistry(reg, iconPacksUI.query)
		for _, rp := range matches {
			groupRow(c, k, "reg-"+rp.ID).Children(func() {
				title := cmp.Or(rp.Name, rp.ID)
				rowLine(c, k, title, func() {
					switch {
					case installedSet[rp.ID]:
						ui.Text(c, locales.SettingsIconPacksInstalledBadge()).FontSize(fontFootnote).FontWeight(600).TextColor(k.accent)
					case installing[rp.ID]:
						ui.Text(c, locales.SettingsIconPacksInstalling()).FontSize(fontFootnote).TextColor(k.textMuted)
					default:
						if ui.Button(c, locales.SettingsIconPacksInstall()).Clicked() {
							a.installIconPack(rp)
						}
					}
				})
				meta := rp.ID
				if rp.Author != "" {
					meta += " · " + rp.Author
				}
				if rp.Version != "" {
					meta += " · " + rp.Version
				}
				if rp.Description != "" {
					meta += " — " + rp.Description
				}
				rowHint(c, k, meta)
			})
		}
	})
}

var _ = (*mygo.Window)(nil)
