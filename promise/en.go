package promise

import "regexp"

// enSubj is "I'll", "I will", "I'm going to" and the same for "we", then the
// small words that may come before the verb.
const enSubj = `\b(?:i|we)(?:'ll| will| shall|'m going to| am going to|'m gonna| am gonna)` +
	`(?:\s+(?:then|also|just|now|both|still|again|keep|continue to|go ahead and|probably|likely|definitely|try to|be|need to|have to|really))*\s+`

var english = &language{
	code: "en",
	skip: regexp.MustCompile(`\b(?:if you(?:'d| would)? (?:want|like|wish|prefer|need|say)|want me to|would you like|do you want|shall i|should i|` +
		`let me know if|i can|i could|i would|i'd|i may|i might|happy to|glad to|feel free|unless|you can|you could|you may|you might|you should|you need to)\b` +
		`|\b(?:not|never|cannot|unable)\b|n't|\bno longer\b`),
	wait: re(
		enSubj+`(?:wait|hold off|sit tight)\b`,
		`\b(?:i|we)(?:'m| am|'re| are)\s+(?:now |still |currently |also )*waiting\b`,
	),
	userWait: regexp.MustCompile(`^\W*(?:` +
		`(?:for|until|till|on|to hear from|to hear)\s+(?:you|your|the user|user|a (?:go|green|reply|response|confirmation|nod)|the (?:go|green|ok|okay|approval|confirmation|answer|reply|response|feedback|decision|user|nod))` +
		`|you\b)`),
	strong: re(
		enSubj+`(?:monitor|watch|track|poll|retry|re-?try|re-?check|recheck|follow up|circle back|get back|come back|report back|check back|check in|check on|check again|`+
			`keep (?:an eye|watching|monitoring|checking|tabs|track|polling|trying|looking)|`+
			`try (?:it |that |this )?again|look (?:at it |into it )?again|ping you|notify you|`+
			`let you know (?:when|once|as soon)|tell you (?:when|once|as soon)|keep you (?:posted|updated|in the loop)|update you (?:when|once|as soon)|`+
			`be (?:monitoring|watching|tracking|checking|polling|keeping an eye))`,
		`\b(?:i|we)(?:'m| am|'re| are)\s+(?:now |still |currently |also )*(?:monitoring|watching|tracking|polling)\b`,
	),
	weak: re(enSubj + `(?:check|verify|look|see|move|restart|re-?run|continue|resume|proceed|pick (?:it|this|that) up|test|fetch|retrieve|merge|deploy|run|send|report|update|try|come|take a look)\b`),
	time: regexp.MustCompile(`\b(?:later|shortly|soon|in a (?:bit|moment|while|minute|few)|` +
		`in (?:a few |about |around |\d+ |some |another |five |ten |two |three |fifteen |twenty |thirty )?(?:seconds?|minutes?|mins?|hours?|hrs?)|` +
		`tomorrow|tonight|this (?:evening|afternoon)|in the (?:next|coming)|after (?:it|that|they) (?:finish|complete|end|is done|are done)|` +
		`(?:once|when|as soon as|after|until|till)\b.{0,50}?\b(?:finish\w*|complet\w*|done|ready|ends?|ended|over|succe\w+|fail\w*|stops?|arriv\w*|lands?|builds?|settles?|passes|turns? green|is up|are up))`),
}
