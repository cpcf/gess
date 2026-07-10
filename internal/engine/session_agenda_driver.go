package engine

// sessionAgendaDriver owns the mutable agenda state that advances rule
// execution for one session.
type sessionAgendaDriver struct {
	agenda            *agenda
	strategy          Strategy
	initialFocusStack []ModuleName
	focusStack        []ModuleName
	ready             bool
	dirty             bool
}

func newSessionAgendaDriver(strategy Strategy) sessionAgendaDriver {
	return sessionAgendaDriver{
		agenda:            newAgendaWithStrategy(strategy),
		strategy:          strategy,
		initialFocusStack: []ModuleName{MainModule},
		focusStack:        []ModuleName{MainModule},
	}
}

func (d *sessionAgendaDriver) cloneForFork(strategy Strategy) sessionAgendaDriver {
	if d == nil {
		return newSessionAgendaDriver(strategy)
	}
	var clonedAgenda *agenda
	if d.agenda != nil {
		clonedAgenda = d.agenda.cloneForFork(strategy)
	}
	return sessionAgendaDriver{
		agenda:            clonedAgenda,
		strategy:          strategy,
		initialFocusStack: cloneModuleNames(d.initialFocusStack),
		focusStack:        cloneModuleNames(d.focusStack),
		ready:             d.ready && !d.dirty,
		dirty:             d.dirty,
	}
}

func (d *sessionAgendaDriver) ensureAgenda() *agenda {
	if d.agenda == nil {
		d.agenda = newAgendaWithStrategy(d.strategy)
	}
	return d.agenda
}

func (d *sessionAgendaDriver) installInitialAgenda(next *agenda) {
	d.agenda = next
	if d.agenda != nil {
		d.agenda.finishInitialTerminalActivations()
	}
	d.markReady()
}

func (d *sessionAgendaDriver) markReady() {
	d.ready = true
	d.dirty = false
}

func (d *sessionAgendaDriver) markUnready() {
	d.ready = false
	d.dirty = false
}

func (d *sessionAgendaDriver) markDirty() {
	d.ready = false
	d.dirty = true
}

func (d *sessionAgendaDriver) isReady() bool {
	return d != nil && d.ready && !d.dirty
}

func (d *sessionAgendaDriver) currentFocus() ModuleName {
	if d == nil || len(d.focusStack) == 0 {
		return MainModule
	}
	module := d.focusStack[len(d.focusStack)-1]
	if module.IsZero() {
		return MainModule
	}
	return module
}

func (d *sessionAgendaDriver) pushFocus(module ModuleName) {
	module = normalizeModuleName(module)
	if d.currentFocus() == module {
		return
	}
	d.focusStack = append(d.focusStack, module)
}

func (d *sessionAgendaDriver) popFocus() ModuleName {
	if d == nil || len(d.focusStack) == 0 {
		return MainModule
	}
	top := d.focusStack[len(d.focusStack)-1]
	if top.IsZero() {
		top = MainModule
	}
	d.focusStack[len(d.focusStack)-1] = ""
	d.focusStack = d.focusStack[:len(d.focusStack)-1]
	return top
}

func (d *sessionAgendaDriver) clearFocusStack() {
	if d == nil {
		return
	}
	for i := range d.focusStack {
		d.focusStack[i] = ""
	}
	d.focusStack = d.focusStack[:0]
}

func (d *sessionAgendaDriver) resetFocusStack() {
	if d == nil {
		return
	}
	d.focusStack = append(d.focusStack[:0], d.initialFocusStack...)
	if len(d.focusStack) == 0 {
		d.focusStack = append(d.focusStack, MainModule)
	}
}
