package promise

import "regexp"

const frSubj = `(?:\bje\s+|\bj')`

var french = &language{
	code:   "fr",
	opener: regexp.MustCompile(`\bne (?:t'inquiete|vous inquietez|t'en fais|vous en faites)(?: pas)?|\bpas de (?:souci|panique|probleme)\b|\bn'hesite\w*`),
	trigger: regexp.MustCompile(`\b(?:dis|dites|previens|prevenez|fais|faites|tiens|tenez)-(?:moi|nous)\b.{0,25}?\b(?:quand|des que|une fois|lorsque)\b` +
		`|\b(?:quand|des que|une fois que|lorsque|apres que)\s+(?:tu|vous)\s+(?:m'|me\s+|nous\s+)?(?:l'|le\s+|la\s+|les\s+)?(?:auras\s+|aurez\s+|as\s+|avez\s+)?(?:envoi\w*|envoy\w*|enver\w*|ecri\w*|dis\w*|dit\b|donn\w*|confirm\w*|repond\w*|transmet\w*|transmis\w*|partag\w*|telecharg\w*|valid\w*|approuv\w*)`),
	skip: regexp.MustCompile(`\b(?:veux-tu|voulez-vous|souhaitez-vous|souhaites-tu|si tu veux|si vous voulez|si tu le souhaites|si vous le souhaitez|si tu preferes|si vous preferez|` +
		`je peux|je pourrais|je pourrai|je voudrais|tu peux|vous pouvez|dis-moi si|dites-moi si|sans)\b`),
	negIn: regexp.MustCompile(`\bpas\b|\bjamais\b`),
	wait:  re(frSubj + `(?:attendrai|attends|vais attendre|patienterai|patiente|reste en attente)\b`),
	userWait: regexp.MustCompile(`^\W*(?:votre|vos|ta|ton|tes|que (?:tu|vous)|de (?:tes|vos)|` +
		`(?:la|le|l'|une|un) ?(?:confirmation|reponse|accord|feu vert|ok|retour)|(?:une |la )?reponse|des nouvelles de (?:toi|vous))\b`),
	strong: re(
		frSubj + `(?:surveillerai|surveille|vais surveiller|garderai un oeil|garde un oeil|vais garder un oeil|garde l'oeil|reessaierai|vais reessayer|` +
			`reviens vers (?:toi|vous)|reviendrai (?:vers|sur) (?:toi|vous|ce)|te tiens au courant|vous tiens au courant|te tiendrai au courant|vous tiendrai au courant|` +
			`vais suivre|suivrai|serai attentif|te previens (?:quand|des que|une fois)|te previendrai|vous previendrai|te recontacte|recontacterai|` +
			`vous previens (?:quand|des que|une fois))\b`,
	),
	weak: re(frSubj + `(?:verifierai|verifie|controlerai|controle|regarderai|regarde|verrai|vais (?:verifier|controler|regarder|voir|deplacer|relancer|redemarrer|continuer|tester|reessayer)|` +
		`deplacerai|deplace|relancerai|relance|redemarrerai|redemarre|continuerai|continue|testerai|teste|reessaie|reprendrai|reprends|mettrai a jour|mets a jour|lancerai|lance|executerai|execute)\b`),
	time: regexp.MustCompile(`(?:\b(?:plus tard|tout a l'heure|bientot|sous peu|d'ici peu|demain|ce soir|cet apres-midi|` +
		`dans (?:un|une|quelques|\d+|deux|trois|cinq|dix|quinze|vingt|trente) (?:instant|moment|petit moment|quart d'heure|secondes?|minutes?|heures?)|` +
		`(?:quand|des que|une fois que|apres que|jusqu'a ce que|lorsque|le temps que)\b.{0,50}?\b(?:fini\w*|termin\w+|complet\w+|pret\w*|fait|acheve\w*|conclu\w*|arriv\w+|echoue|reussi\w*|passe\w*))\b)`),
}
