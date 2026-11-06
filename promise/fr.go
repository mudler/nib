package promise

import "regexp"

const frSubj = `(?:\bje\s+|\bj')`

var french = &language{
	code: "fr",
	skip: regexp.MustCompile(`\b(?:veux-tu|voulez-vous|souhaitez-vous|souhaites-tu|si tu veux|si vous voulez|si tu le souhaites|si vous le souhaitez|si tu preferes|si vous preferez|` +
		`je peux|je pourrais|je pourrai|je voudrais|tu peux|vous pouvez|dis-moi si|dites-moi si|ne|pas|jamais|sans|aucun\w*)\b|\bn'|\bn'hesit`),
	wait: re(frSubj + `(?:attendrai|attends|vais attendre|patienterai|patiente|reste en attente)\b`),
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
