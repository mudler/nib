package promise

import (
	"regexp"
	"strings"
)

var italian = &language{
	code: "it",
	prep: func(s string) string { return strings.ReplaceAll(s, "non appena", "appena") },
	skip: regexp.MustCompile(`\b(?:vuoi|vuole|volete|posso|potrei|potrebbe|potremmo|vorrei|puoi|puo|non|mai|senza|nessun\w*|ti aspetto|ti attendo)\b` +
		`|\bse (?:vuoi|vuole|preferisci|preferite|desideri|ti serve|ti va|serve)\b|\b(?:fammi|dimmi) sapere se\b|\bdimmi se\b`),
	wait: re(
		`\b(?:aspett(?:o|ero|eremo|iamo)|attend(?:o|ero|iamo)|sto aspettando|sto attendendo|rimango in attesa|resto in attesa)\b`,
	),
	userWait: regexp.MustCompile(`^\W*(?:che (?:tu|voi|lei|mi|ci|dica|dia|confermi|risponda)\b|di (?:avere |ricevere )?(?:tue|tua|tuo|tuoi)\b|` +
		`(?:la|il|le|i|una|un) (?:tua|tuo|tue|tuoi|conferma|risposta|riscontro|feedback|via libera|ok)\b|tue|tua|tuo|tuoi|conferma|risposta|riscontro|feedback|istruzioni|il via|via libera|ok\b)`),
	strong: re(
		`\b(?:monitor(?:o|ero|eremo)|sto monitorando|tengo d'occhio|terro d'occhio|terremo d'occhio|tengo sotto controllo|sorveglio|sorvegliero)\b`,
		`\b(?:ricontroll(?:ero|eremo)|riprov(?:ero|eremo)|riverific(?:hero|heremo)|tornero a|torno a (?:controllare|verificare)|ricontattero)\b`,
		`\bti (?:faccio sapere|avviso|avvisero|aggiorno|aggiornero|scrivo|scrivero|dico|tengo aggiornato|terro aggiornato)\b.{0,20}\b(?:quando|appena|una volta|tra|fra|piu tardi|dopo)\b`,
	),
	weak: re(`\b(?:controll(?:o|ero|eremo)|verific(?:o|hero|heremo)|vedro|sposto|spostero|riavvi(?:o|ero)|rilanc(?:io|ero)|riprov(?:o)|ricontroll(?:o)|proced(?:o|ero)|continu(?:o|ero)|prov(?:o|ero)|lancero|aggiorn(?:o|ero)|riprend(?:o|ero)|ritorno|torno|riguardo|riguardero)\b`),
	time: regexp.MustCompile(`\b(?:tra poco|fra poco|tra un po|fra un po|piu tardi|a breve|tra breve|fra breve|a momenti|domani|stasera|stanotte|in seguito|` +
		`(?:tra|fra) (?:un|una|qualche|alcuni|alcune|\d+|due|tre|cinque|dieci|quindici|venti|trenta|mezz') ?(?:po'?|second\w*|minut\w*|or[ae]|attim\w*|moment\w*|giorn\w*|settiman\w*)|` +
		`(?:quando|appena|una volta che|dopo che|finche|fino a quando|fino a che)\b.{0,50}?\b(?:finisc\w+|termin\w+|compl\w+|pront\w+|fatto|ha finito|e finit\w+|sia finit\w+|sara finit\w+|conclud\w+|arriv\w+|riesc\w+|va a buon fine|fallisc\w+|e pronto))`),
}
