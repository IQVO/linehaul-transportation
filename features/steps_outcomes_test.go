package features_test

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
)

func registerOutcomeSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the command succeeds$`, w.commandSucceeds)
	sc.Step(`^the command is refused with "([^"]*)"$`, w.commandRefusedWith)
	sc.Step(`^the last command published the event "([^"]*)"$`, w.publishedEvent)
	sc.Step(`^no event was published by the last command$`, w.publishedNothing)
}

func (w *world) commandSucceeds() error {
	w.errInspected = true
	if w.lastErr != nil {
		return fmt.Errorf("the command failed: %w", w.lastErr)
	}
	return nil
}

func (w *world) commandRefusedWith(slug string) error {
	w.errInspected = true
	want, ok := slugs[slug]
	if !ok {
		return fmt.Errorf("the suite knows no problem %q", slug)
	}
	if w.lastErr == nil {
		return fmt.Errorf("the command succeeded, want it refused with %q", slug)
	}
	if !errors.Is(w.lastErr, want) {
		return fmt.Errorf("the command failed with %v, want %q", w.lastErr, slug)
	}
	return nil
}

func (w *world) publishedEvent(name string) error {
	for _, got := range w.publishedNames() {
		if got == name {
			return nil
		}
	}
	return fmt.Errorf("the last command published [%s], not %s", strings.Join(w.publishedNames(), " "), name)
}

func (w *world) publishedNothing() error {
	if names := w.publishedNames(); len(names) > 0 {
		return fmt.Errorf("the last command published %v, want nothing", names)
	}
	return nil
}
