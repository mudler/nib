// Package promise detects when an assistant message promises to do something
// later that needs monitoring or waiting: "I'll check on it shortly", "I'll
// wait and move it when it finishes", "I'll retry later".
//
// A host uses it at the end of a turn. If the final text makes such a promise
// and the host has armed nothing that could keep it (a timer, a watcher, a
// running job), the promise is empty: nothing will ever run. The host can then
// ask the model to arm something or to say plainly that it cannot.
//
// The matcher reads English, Italian, Spanish, French and German. It is
// conservative on purpose, because a false alarm costs more than a missed one.
// A sentence matches only when the speaker is the first person and the verb
// is future, progressive or (in Italian, Spanish, French and German) present
// with a time word, and the verb is about waiting, watching or checking back.
// These are ignored: questions, offers ("I can check it if you want"),
// a negated promise verb ("I won't check"), waiting for the user, an action
// that starts when the person tells or sends something ("tell me when it is
// finished and I will move the file"), quoted text, code and block quotes, and
// past tense. A negation that does not touch the promise verb, as in "don't
// worry, I'll check later", does not cancel the promise. Only the last few sentences of the text are read, since the
// promise that matters is the one the turn ends on.
//
// The package uses only the standard library and holds no state, so it is safe
// for concurrent use.
package promise
