/*
 * Copyright (C) 2020-2022, IrineSistiana
 *
 * This file is part of mosdns.
 *
 * mosdns is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * mosdns is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package coremain

import (
	"fmt"
	"github.com/IrineSistiana/mosdns/v5/pkg/utils"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"reflect"
	"sync"
)

// NewPluginArgsFunc represents a func that creates a new args object.
type NewPluginArgsFunc func() any

// NewPluginFunc represents a func that can init a Plugin.
// args is the object created by NewPluginArgsFunc.
type NewPluginFunc func(bp *BP, args any) (p any, err error)

type PluginTypeInfo struct {
	NewPlugin NewPluginFunc
	NewArgs   NewPluginArgsFunc
}

var (
	// pluginTypeRegister stores init funcs for certain plugin types
	pluginTypeRegister struct {
		sync.RWMutex
		m map[string]PluginTypeInfo
	}
)

// RegNewPluginFunc registers the type.
// If the type has been registered. RegNewPluginFunc will panic.
func RegNewPluginFunc(typ string, initFunc NewPluginFunc, argsType NewPluginArgsFunc) {
	pluginTypeRegister.Lock()
	defer pluginTypeRegister.Unlock()

	_, ok := pluginTypeRegister.m[typ]
	if ok {
		panic(fmt.Sprintf("duplicate plugin type [%s]", typ))
	}

	if pluginTypeRegister.m == nil {
		pluginTypeRegister.m = make(map[string]PluginTypeInfo)
	}
	pluginTypeRegister.m[typ] = PluginTypeInfo{
		NewPlugin: initFunc,
		NewArgs:   argsType,
	}
}

// DelPluginType deletes the init func for this plugin type.
// It is a noop if pluginType is not registered.
func DelPluginType(typ string) {
	pluginTypeRegister.Lock()
	defer pluginTypeRegister.Unlock()
	delete(pluginTypeRegister.m, typ)
}

// GetPluginType gets the registered type init func.
func GetPluginType(typ string) (PluginTypeInfo, bool) {
	pluginTypeRegister.RLock()
	defer pluginTypeRegister.RUnlock()

	info, ok := pluginTypeRegister.m[typ]
	return info, ok
}

// NewPlugin initializes a Plugin from c and adds it to mosdns.
func (m *Mosdns) NewPlugin(c PluginConfig) error {
	if _, dup := m.plugins[c.Tag]; dup {
		return fmt.Errorf("duplicated plugin tag %s", c.Tag)
	}

	typeInfo, ok := GetPluginType(c.Type)
	if !ok {
		return fmt.Errorf("plugin type %s not defined", c.Type)
	}

	args := typeInfo.NewArgs()
	if reflect.TypeOf(c.Args) == reflect.TypeOf(args) { // Same type, no need to parse.
		args = c.Args
	} else {
		if err := utils.WeakDecode(c.Args, args); err != nil {
			return fmt.Errorf("unable to decode plugin args: %w", err)
		}
	}
	p, err := typeInfo.NewPlugin(NewBP(c.Tag, m), c.Args)
	if err != nil {
		return fmt.Errorf("failed to init plugin: %w", err)
	}

	m.plugins[c.Tag] = &pluginWrapper{
		tag: c.Tag,
		typ: c.Type,
		p:   p,
	}
	return nil
}

// GetAllPluginTypes returns all plugin types which are configurable.
func GetAllPluginTypes() []string {
	pluginTypeRegister.RLock()
	defer pluginTypeRegister.RUnlock()

	var t []string
	for typ := range pluginTypeRegister.m {
		t = append(t, typ)
	}
	return t
}

type NewPersetPluginFunc func(bp *BP) (any, error)

var presetPluginFuncReg struct {
	sync.Mutex
	m map[string]NewPersetPluginFunc
}

func RegNewPersetPluginFunc(tag string, f NewPersetPluginFunc) {
	presetPluginFuncReg.Lock()
	defer presetPluginFuncReg.Unlock()
	if _, ok := presetPluginFuncReg.m[tag]; ok {
		panic(fmt.Sprintf("preset plugin %s has already been registered", tag))
	}
	if presetPluginFuncReg.m == nil {
		presetPluginFuncReg.m = make(map[string]NewPersetPluginFunc)
	}
	presetPluginFuncReg.m[tag] = f
}

func LoadNewPersetPluginFuncs() map[string]NewPersetPluginFunc {
	presetPluginFuncReg.Lock()
	defer presetPluginFuncReg.Unlock()
	m := make(map[string]NewPersetPluginFunc)
	for tag, pluginFunc := range presetPluginFuncReg.m {
		m[tag] = pluginFunc
	}
	return m
}

type BP struct {
	tag string
	m   *Mosdns
	l   *zap.Logger
}

// NewBP creates a new BP. m MUST NOT nil.
func NewBP(tag string, m *Mosdns) *BP {
	return &BP{
		tag: tag,
		l:   m.Logger().Named(tag),
		m:   m,
	}
}

// L returns a non-nil logger.
func (p *BP) L() *zap.Logger {
	return p.l
}

// M returns a non-nil Mosdns.
func (p *BP) M() *Mosdns {
	return p.m
}

// GetMetricsReg return a prometheus.Registerer with a prefix of "mosdns_plugin_${plugin_tag}_]"
func (p *BP) GetMetricsReg() prometheus.Registerer {
	return prometheus.WrapRegistererWithPrefix(fmt.Sprintf("plugin_%s_", p.tag), p.m.GetMetricsReg())
}

// RegAPI mounts mux to mosdns api. Note: Plugins MUST NOT call RegAPI twice.
// Since mounting same path to root chi.Mux causes runtime panic.
func (p *BP) RegAPI(mux *chi.Mux) {
	p.m.RegPluginAPI(p.tag, mux)
}
