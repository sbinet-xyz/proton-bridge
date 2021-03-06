// Copyright (c) 2020 Proton Technologies AG
//
// This file is part of ProtonMail Bridge.
//
// ProtonMail Bridge is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// ProtonMail Bridge is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with ProtonMail Bridge.  If not, see <https://www.gnu.org/licenses/>.

package listener

import (
	"sync"
	"time"

	"github.com/ProtonMail/proton-bridge/pkg/config"
)

var log = config.GetLogEntry("bridgeUtils/listener") //nolint[gochecknoglobals]

// Listener has a list of channels watching for updates.
type Listener interface {
	SetLimit(eventName string, limit time.Duration)
	Add(eventName string, channel chan<- string)
	Remove(eventName string, channel chan<- string)
	Emit(eventName string, data string)
	SetBuffer(eventName string)
	RetryEmit(eventName string)
}

type listener struct {
	channels  map[string][]chan<- string
	limits    map[string]time.Duration
	lastEmits map[string]map[string]time.Time
	buffered  map[string][]string
	lock      *sync.RWMutex
}

// New returns a new Listener which initially has no topics.
func New() Listener {
	return &listener{
		channels:  nil,
		limits:    make(map[string]time.Duration),
		lastEmits: make(map[string]map[string]time.Time),
		buffered:  make(map[string][]string),
		lock:      &sync.RWMutex{},
	}
}

// SetLimit sets the limit for the `eventName`. When the same event (name and data)
// is emitted within last time duration (`limit`), event is dropped. Zero limit clears
// the limit for the specific `eventName`.
func (l *listener) SetLimit(eventName string, limit time.Duration) {
	if limit == 0 {
		delete(l.limits, eventName)
		return
	}
	l.limits[eventName] = limit
}

// Add adds an event listener.
func (l *listener) Add(eventName string, channel chan<- string) {
	l.lock.Lock()
	defer l.lock.Unlock()

	if l.channels == nil {
		l.channels = make(map[string][]chan<- string)
	}
	if _, ok := l.channels[eventName]; ok {
		l.channels[eventName] = append(l.channels[eventName], channel)
	} else {
		l.channels[eventName] = []chan<- string{channel}
	}
}

// Remove removes an event listener.
func (l *listener) Remove(eventName string, channel chan<- string) {
	l.lock.Lock()
	defer l.lock.Unlock()

	if _, ok := l.channels[eventName]; ok {
		for i := range l.channels[eventName] {
			if l.channels[eventName][i] == channel {
				l.channels[eventName] = append(l.channels[eventName][:i], l.channels[eventName][i+1:]...)
				break
			}
		}
	}
}

// Emit emits an event in parallel to all listeners (channels).
func (l *listener) Emit(eventName string, data string) {
	l.emit(eventName, data, false)
}

func (l *listener) emit(eventName, data string, isReEmit bool) {
	l.lock.RLock()
	defer l.lock.RUnlock()

	if !l.shouldEmit(eventName, data) {
		log.Warn("Emit of ", eventName, " with data ", data, " skipped")
		return
	}

	if _, ok := l.channels[eventName]; ok {
		for i, handler := range l.channels[eventName] {
			go func(handler chan<- string, i int) {
				handler <- data
				log.Debugf("emitted %s data %s -> %d", eventName, data, i)
			}(handler, i)
		}
	} else if !isReEmit {
		if bufferedData, ok := l.buffered[eventName]; ok {
			l.buffered[eventName] = append(bufferedData, data)
			log.Debugf("Buffering event %s data %s", eventName, data)
		} else {
			log.Warnf("No channel is listening to %s data %s", eventName, data)
		}
	}
}

func (l *listener) shouldEmit(eventName, data string) bool {
	if _, ok := l.limits[eventName]; !ok {
		return true
	}

	l.clearLastEmits()

	if eventLastEmits, ok := l.lastEmits[eventName]; ok {
		if _, ok := eventLastEmits[data]; ok {
			return false
		}
	} else {
		l.lastEmits[eventName] = make(map[string]time.Time)
	}

	l.lastEmits[eventName][data] = time.Now()
	return true
}

func (l *listener) clearLastEmits() {
	for eventName, lastEmits := range l.lastEmits {
		limit, ok := l.limits[eventName]
		if !ok { // Limits were disabled.
			delete(l.lastEmits, eventName)
			continue
		}
		for key, lastEmit := range lastEmits {
			if time.Since(lastEmit) > limit {
				delete(lastEmits, key)
			}
		}
	}
}

func (l *listener) SetBuffer(eventName string) {
	if _, ok := l.buffered[eventName]; !ok {
		l.buffered[eventName] = []string{}
	}
}

func (l *listener) RetryEmit(eventName string) {
	if _, ok := l.channels[eventName]; !ok || len(l.channels[eventName]) == 0 {
		return
	}
	if bufferedData, ok := l.buffered[eventName]; ok {
		for _, data := range bufferedData {
			l.emit(eventName, data, true)
		}
		l.buffered[eventName] = []string{}
	}
}
