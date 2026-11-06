package promise

import (
	"regexp"
	"strings"
)

// deAscii folds the "ae", "oe" and "ue" spelling of umlauts to the same form
// as the folded umlauts.
var deAscii = strings.NewReplacer("ae", "a", "oe", "o", "ue", "u")

var german = &language{
	code:   "de",
	prep:   func(s string) string { return deAscii.Replace(s) },
	must:   regexp.MustCompile(`\bich\b`),
	opener: regexp.MustCompile(`\b(?:mach dir |machen sie sich |macht euch )?keine (?:sorge|sorgen|angst|panik)\b|\bkein problem\b`),
	trigger: regexp.MustCompile(`\b(?:sag|sagt|gib|gebt|schreib|schreibt)\w*\s+(?:mir|uns)\b.{0,25}?\b(?:wenn|sobald|nachdem)\b|\bmelde(?:t)? (?:dich|euch)\b|\bmelden sie sich\b` +
		`|\b(?:sobald|wenn|nachdem)\s+(?:du|sie|ihr)\s+(?:mir\s+|uns\s+)?(?:\w+\s+){0,3}?(?:schickst|sendest|schickt|sendet|schicken|senden|gibst|sagst|schreibst|bestatigst|antwortest|hochladst|ubermittelst|zeigst|freigibst|geschickt|gesendet|gegeben|gesagt|bestatigt|geantwortet|hochgeladen|ubermittelt|bescheid gibst)\b`),
	skip: regexp.MustCompile(`\b(?:soll ich|mochtest du|mochten sie|willst du|wollen sie|ich kann|ich konnte|ich wurde|ohne)\b` +
		`|\bwenn (?:du|sie) (?:willst|wollen|mochtest|mochten|magst)\b|\bfalls (?:du|sie)\b|\b(?:sag|sagen sie|lass) (?:mir|es mich|mich)\b`),
	negBefore: regexp.MustCompile(`\b(?:nicht|nie|niemals|kein\w*)\s+(?:\w+\s+){0,2}$`),
	negIn:     regexp.MustCompile(`\b(?:nicht|nie|niemals|kein\w*)\b`),
	wait:      re(`\b(?:warte|werde warten|werde abwarten|warte ab)\b`),
	userWait:  regexp.MustCompile(`^\W*(?:ab\b\W*)?(?:auf (?:deine|deinen|dein|dir|ihre|ihren|ihr|sie|euch|eure|eine|ein|die (?:antwort|bestatigung|freigabe|ruckmeldung|ruckspra)\w*)|bis (?:du|sie|ihr)\b|(?:deine|ihre) )`),
	strong: re(
		`\b(?:beobachte|uberwache|werde\b.{0,30}?\b(?:beobachten|uberwachen)|melde mich|werde mich melden|bleibe dran|werde dranbleiben)\b`,
		`\b(?:halte|behalte)\b.{0,30}?\bim auge\b|\bwerde\b.{0,30}?\bim auge behalten\b`,
		`\b(?:gebe|sage) (?:dir |ihnen |euch )?bescheid\b|\bwerde (?:dir |ihnen |euch )?bescheid (?:geben|sagen)\b`,
		`\b(?:versuche|probiere) (?:es )?(?:spater|nochmal|erneut|noch einmal)\b|\bwerde (?:es )?(?:erneut|nochmal|noch einmal|spater) versuchen\b`,
		`\bschaue? (?:spater|gleich|nochmal|erneut) nach\b|\bwerde\b.{0,30}?\bnachsehen\b`,
	),
	weak: re(`\b(?:pruf\w*|kontrollier\w*|nachsehe|nachschau\w*|schaue?|sehe|verschieb\w*|starte|neustart\w*|wiederhol\w*|aktualisier\w*|fortsetz\w*|weitermach\w*|teste|versuch\w*)\b`),
	time: regexp.MustCompile(`\b(?:spater|nachher|gleich|in kurze|bald|demnachst|heute abend|heute nacht|morgen|` +
		`in (?:einem|einer|ein paar|ein|\d+|zwei|drei|funf|zehn|funfzehn|zwanzig|dreissig) (?:augenblick|moment|minute\w*|sekunde\w*|stunde\w*|weile|bisschen)|` +
		`(?:sobald|wenn|nachdem|bis|solange)\b.{0,60}?\b(?:fertig|abgeschlossen|durch|beendet|erledigt|bereit|fertiggestellt|gelaufen|angekommen|erfolgreich|fehlschlagt))`),
}
