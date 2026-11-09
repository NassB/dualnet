// Package notify tells the user, with a Windows notification, when a link drops,
// comes back or appears — useful when dualnet's window is closed.
package notify

import (
	"fmt"
	"sync"
	"time"

	"github.com/NassB/dualnet/internal/links"
)

// Notifier turns link events into notifications.
type Notifier struct {
	Enabled func() bool                    // nil = always
	Send    func(title, body string) error // default: Send (native notification)
	French  bool                           // French texts, English otherwise

	// MinGap is the minimum time between two notifications for the same link,
	// so a link that flaps does not flood the screen (default 20 s).
	MinGap time.Duration

	mu   sync.Mutex
	last map[string]time.Time
	now  func() time.Time
}

func (n *Notifier) tr(en, fr string) string {
	if n.French {
		return fr
	}
	return en
}

// message returns the notification text for an event, or ok=false when the
// event is not worth a notification. up is how many links are usable now.
func (n *Notifier) message(name, event string, up int) (body string, ok bool) {
	switch event {
	case links.EventAdded, links.EventReconnected:
		if up >= 2 {
			return fmt.Sprintf(n.tr("Link “%s” is connected: combining %d links.", "Lien « %s » connecté : %d liens combinés."), name, up), true
		}
		return fmt.Sprintf(n.tr("Link “%s” is connected.", "Lien « %s » connecté."), name), true
	case links.EventDisconnected:
		if up == 0 {
			return fmt.Sprintf(n.tr("Link “%s” disconnected. No link left.", "Lien « %s » déconnecté. Plus aucun lien."), name), true
		}
		return fmt.Sprintf(n.tr("Link “%s” disconnected. dualnet keeps using the other links.", "Lien « %s » déconnecté. dualnet continue avec les autres liens."), name), true
	case links.EventNotAnswering:
		return fmt.Sprintf(n.tr("Link “%s” stopped reaching the internet and is skipped for now.", "Le lien « %s » n’accède plus à internet : il est ignoré pour l’instant."), name), true
	case links.EventBudgetWarn:
		return fmt.Sprintf(n.tr("Link “%s” has reached the warning level of its monthly data budget.", "Le lien « %s » a atteint le seuil d’alerte de son budget de données mensuel."), name), true
	case links.EventOverBudget:
		return fmt.Sprintf(n.tr("Link “%s” reached its monthly data budget and is no longer used.", "Le lien « %s » a atteint son budget de données mensuel et n’est plus utilisé."), name), true
	case links.EventBudgetKept:
		return fmt.Sprintf(n.tr("Link “%s” is over its monthly budget but stays in use: it is the only link left.", "Le lien « %s » a dépassé son budget mensuel mais reste utilisé : c’est le seul lien restant."), name), true
	case links.EventBudgetOK:
		return fmt.Sprintf(n.tr("Link “%s” is available again: its monthly budget was reset.", "Le lien « %s » est de nouveau disponible : son budget mensuel est remis à zéro."), name), true
	case links.EventAnswering:
		return fmt.Sprintf(n.tr("Link “%s” reaches the internet again.", "Le lien « %s » accède de nouveau à internet."), name), true
	}
	return "", false
}

// LinkEvent notifies about one event (when enabled and not repeating too fast).
func (n *Notifier) LinkEvent(name, event string, up int) {
	if n.Enabled != nil && !n.Enabled() {
		return
	}
	body, ok := n.message(name, event, up)
	if !ok {
		return
	}
	n.mu.Lock()
	if n.last == nil {
		n.last = map[string]time.Time{}
	}
	now := time.Now
	if n.now != nil {
		now = n.now
	}
	gap := n.MinGap
	if gap == 0 {
		gap = 20 * time.Second
	}
	if t, seen := n.last[name]; seen && now().Sub(t) < gap {
		n.mu.Unlock()
		return
	}
	n.last[name] = now()
	n.mu.Unlock()
	n.send("dualnet", body)
}

// Test sends a notification now, whatever the settings, and returns any error.
func (n *Notifier) Test() error {
	return n.sendErr("dualnet", n.tr("This is a test notification.", "Ceci est une notification de test."))
}

func (n *Notifier) send(title, body string) { go n.sendErr(title, body) }

func (n *Notifier) sendErr(title, body string) error {
	if n.Send != nil {
		return n.Send(title, body)
	}
	return Send(title, body)
}
