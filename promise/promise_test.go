package promise

import "testing"

type tcase struct {
	text string
	want bool
}

func run(t *testing.T, cases []tcase) {
	t.Helper()
	for _, c := range cases {
		m, got := Find(c.text)
		if got != c.want {
			t.Errorf("Find(%q) = %v (%+v), want %v", c.text, got, m, c.want)
		}
		if Promises(c.text) != got {
			t.Errorf("Promises(%q) disagrees with Find", c.text)
		}
	}
}

func TestEnglish(t *testing.T) {
	run(t, []tcase{
		{"The build is running. I'll check on it shortly.", true},
		{"I'll wait and move it when it finishes.", true},
		{"I will retry later.", true},
		{"I'll keep an eye on the deploy.", true},
		{"I'm going to monitor the queue.", true},
		{"I'm monitoring the job now.", true},
		{"I'll get back to you once the tests are done.", true},
		{"I'll let you know when it completes.", true},
		{"I'll check back in a few minutes.", true},
		{"We'll try again tomorrow.", true},
		{"I'll run the migration when the backup finishes.", true},
		{"I'll circle back after the release.", true},
		{"I'll be watching the logs.", true},
		{"Done for now. I'll verify it in 10 minutes.", true},
		{"I'm waiting for the download to finish.", true},
		{"I'll then wait until the pipeline is green and merge it, once it is done.", true},
		{"Everything is set up.\n\nI'll follow up as soon as the job ends.", true},

		{"Do you want me to check later?", false},
		{"Should I check back in an hour?", false},
		{"I can check it if you want.", false},
		{"I could monitor it for you.", false},
		{"If you like, I'll check on it later.", false},
		{"Let me know if you want me to retry later.", false},
		{"I'll wait for your confirmation.", false},
		{"I'll wait until you approve.", false},
		{"I'm waiting for your reply.", false},
		{"I checked the logs earlier and it passed.", false},
		{"I waited for the build and it finished.", false},
		{"I retried it twice.", false},
		{"I can't monitor it after this turn.", false},
		{"I won't be able to check later.", false},
		{"I will not retry.", false},
		{"I'll check the file and then summarize it.", false},
		{"I'll explain the design below.", false},
		{"Here is the result of the check.", false},
		{"You can check back later.", false},
		{"The user said \"I'll check on it shortly\" in the ticket.", false},
		{"> I'll check on it shortly", false},
		{"```\n# I'll check later\nsleep 10\n```", false},
		{"Run `wait` then `retry later` in the shell.", false},
		{"It will finish soon.", false},
		{"", false},
		{"Done.", false},
	})
}

func TestEnglishOnlyLastSentencesRead(t *testing.T) {
	text := "I'll check on it shortly. One. Two. Three. Four. Five."
	if Promises(text) {
		t.Error("a promise far from the end should be ignored")
	}
	if !Promises("One. Two. Three. I'll check on it shortly.") {
		t.Error("a promise at the end should match")
	}
}

func TestItalian(t *testing.T) {
	run(t, []tcase{
		{"Controllo tra poco.", true},
		{"Aspetto che finisca e poi lo sposto.", true},
		{"Ricontrollerò più tardi.", true},
		{"Monitorerò il job.", true},
		{"Tengo d'occhio il deploy.", true},
		{"Riprovo fra qualche minuto.", true},
		{"Ti faccio sapere appena finisce.", true},
		{"Ti avviso non appena è pronto.", true},
		{"Verificherò quando il build termina.", true},
		{"Sto monitorando il processo.", true},
		{"Aspetterò che il download sia completato.", true},
		{"Il job è partito. Controllerò il risultato a breve.", true},
		{"Sposto i file quando ha finito.", true},
		{"Controllo i log domani mattina.", true},

		{"Vuoi che controlli tra poco?", false},
		{"Se vuoi controllo tra poco.", false},
		{"Posso controllare più tardi.", false},
		{"Potrei aspettare che finisca.", false},
		{"Aspetto la tua conferma.", false},
		{"Aspetto che tu mi dica come procedere.", false},
		{"Ti aspetto.", false},
		{"Ho controllato i log ed è tutto a posto.", false},
		{"Non posso controllare più tardi.", false},
		{"Non controllo tra poco.", false},
		{"Ho aspettato che finisse.", false},
		{"Il controllo è passato.", false},
		{"Ecco il risultato.", false},
		{"Ha detto \"controllo tra poco\" nel messaggio.", false},
		{"> Controllo tra poco", false},
		{"Dimmi se vuoi che riprovi più tardi.", false},
	})
}

func TestSpanish(t *testing.T) {
	run(t, []tcase{
		{"Revisaré más tarde.", true},
		{"Esperaré a que termine y luego lo muevo.", true},
		{"Reintentaré en unos minutos.", true},
		{"Estaré pendiente del despliegue.", true},
		{"Te aviso cuando termine.", true},
		{"Voy a monitorear el trabajo.", true},
		{"Vuelvo a revisar en breve.", true},
		{"Compruebo el resultado cuando acabe.", true},
		{"Estoy monitoreando el proceso.", true},
		{"Voy a esperar a que termine la compilación.", true},

		{"¿Quieres que revise más tarde?", false},
		{"Si quieres, reviso más tarde.", false},
		{"Puedo revisarlo más tarde.", false},
		{"Esperaré tu confirmación.", false},
		{"Esperaré a que me confirmes.", false},
		{"Espero que esto ayude.", false},
		{"Ya revisé los registros.", false},
		{"No puedo revisar más tarde.", false},
		{"Aquí está el resultado.", false},
		{"Dijo \"revisaré más tarde\" en el correo.", false},
		{"Luego te cuento cómo quedó el código en el repositorio, con calma y sin prisa, ya veremos qué pasa y cómo sigue todo.", false},
	})
}

func TestFrench(t *testing.T) {
	run(t, []tcase{
		{"Je vérifierai plus tard.", true},
		{"J'attendrai que ça finisse, puis je le déplace.", true},
		{"Je réessaierai dans quelques minutes.", true},
		{"Je surveille le déploiement.", true},
		{"Je te tiens au courant.", true},
		{"Je vais surveiller le job.", true},
		{"Je reviens vers vous dès que c'est terminé.", true},
		{"Je regarde tout à l'heure.", true},
		{"J'attends que le téléchargement soit terminé.", true},
		{"Je garderai un oeil sur la file.", true},

		{"Veux-tu que je vérifie plus tard ?", false},
		{"Si tu veux, je vérifie plus tard.", false},
		{"Je peux vérifier plus tard.", false},
		{"J'attends votre confirmation.", false},
		{"J'attends que tu me dises.", false},
		{"J'ai vérifié les logs.", false},
		{"Je ne peux pas vérifier plus tard.", false},
		{"Voici le résultat.", false},
		{"Il a écrit \"je vérifierai plus tard\" dans le mail.", false},
	})
}

func TestGerman(t *testing.T) {
	run(t, []tcase{
		{"Ich prüfe das später.", true},
		{"Ich werde es später prüfen.", true},
		{"Ich warte, bis der Build fertig ist, und verschiebe es dann.", true},
		{"Ich melde mich, sobald es fertig ist.", true},
		{"Ich behalte den Job im Auge.", true},
		{"Ich versuche es später erneut.", true},
		{"Ich schaue gleich nach.", true},
		{"Ich werde den Deploy beobachten.", true},
		{"Ich pruefe das spaeter.", true},
		{"Ich gebe Bescheid, wenn es durch ist.", true},

		{"Soll ich später prüfen?", false},
		{"Wenn du willst, prüfe ich das später.", false},
		{"Ich kann das später prüfen.", false},
		{"Ich warte auf deine Antwort.", false},
		{"Ich warte, bis du bestätigst.", false},
		{"Ich habe die Logs geprüft.", false},
		{"Ich werde es nicht später prüfen.", false},
		{"Hier ist das Ergebnis.", false},
		{"Er schrieb \"ich prüfe das später\" in der Mail.", false},
		{"Das prüft der Test später.", false},
	})
}

func TestLanguageReported(t *testing.T) {
	cases := map[string]string{
		"I'll check on it shortly.": "en",
		"Controllo tra poco.":       "it",
		"Revisaré más tarde.":       "es",
		"Je vérifierai plus tard.":  "fr",
		"Ich prüfe das später.":     "de",
	}
	for text, lang := range cases {
		m, ok := Find(text)
		if !ok || m.Language != lang {
			t.Errorf("Find(%q) = %+v, %v; want language %s", text, m, ok, lang)
		}
		if ok && m.Sentence == "" {
			t.Errorf("Find(%q): empty sentence", text)
		}
	}
}

func TestLongTextDoesNotPanic(t *testing.T) {
	long := ""
	for i := 0; i < 5000; i++ {
		long += "Some filler text here. "
	}
	if Promises(long) {
		t.Error("filler should not match")
	}
	if !Promises(long + "I'll check on it shortly.") {
		t.Error("promise at the end of a long text should match")
	}
}

// A statement conditioned on the person telling or sending something waits
// for that message, so it is not an empty promise.
func TestUserTriggerIsNotAPromise(t *testing.T) {
	run(t, []tcase{
		// English
		{"Tell me when it is finished and I will move the file.", false},
		{"Let me know when the download ends, and I'll verify it shortly.", false},
		{"Once you send the file, I'll check it shortly.", false},
		{"As soon as you've shared the logs I will look at them later.", false},
		{"I'll check when it finishes.", true},
		{"I'll let you know when it finishes.", true},
		{"The upload runs now. I'll check it once it is done.", true},
		// Italian
		{"Dimmi quando hai finito e sposto il file.", false},
		{"Fammi sapere quando è pronto e controllo tra poco.", false},
		{"Appena mi invii il file, controllo tra poco.", false},
		{"Quando mi mandi i log li verifico a breve.", false},
		{"Controllo quando il job finisce.", true},
		{"Ti faccio sapere appena finisce.", true},
		// Spanish
		{"Dime cuando termine y muevo el archivo.", false},
		{"Avísame cuando esté listo y reviso más tarde.", false},
		{"En cuanto me envíes el archivo, lo reviso en breve.", false},
		{"Cuando me confirmes, compruebo el resultado cuando acabe.", false},
		{"Reviso el resultado cuando acabe.", true},
		{"Te aviso cuando termine.", true},
		// French
		{"Dis-moi quand c'est fini et je déplacerai le fichier.", false},
		{"Préviens-moi quand c'est prêt, je vérifierai plus tard.", false},
		{"Dès que tu m'envoies le fichier, je vérifie plus tard.", false},
		{"Une fois que vous m'aurez envoyé les logs, je regarderai tout à l'heure.", false},
		{"Je vérifierai quand le job sera terminé.", true},
		{"Je te tiens au courant.", true},
		// German
		{"Sag mir Bescheid, wenn es fertig ist, dann verschiebe ich die Datei.", false},
		{"Gib mir Bescheid, sobald du fertig bist, ich prüfe das dann später.", false},
		{"Sobald du mir die Datei schickst, prüfe ich das später.", false},
		{"Wenn du mir die Logs gesendet hast, schaue ich gleich nach.", false},
		{"Ich prüfe das, sobald der Job fertig ist.", true},
		{"Ich gebe Bescheid, wenn es durch ist.", true},
	})
}

// A negation cancels the promise only when it applies to the promise verb. A
// reassurance such as "don't worry" does not.
func TestNegationScope(t *testing.T) {
	run(t, []tcase{
		// English
		{"Don't worry, I'll check later.", true},
		{"No worries, I'll retry in a few minutes.", true},
		{"It did not fail, I'll check back shortly.", true},
		{"I can't do more right now, but I'll check later.", true},
		{"I won't check later.", false},
		{"I will not retry.", false},
		{"I'm not going to monitor it.", false},
		{"I can't monitor it after this turn.", false},
		// Italian
		{"Non preoccuparti, controllo tra poco.", true},
		{"Non ti preoccupare, ricontrollerò più tardi.", true},
		{"Non è ancora finito, controllo tra poco.", true},
		{"Non controllerò tra poco.", false},
		{"Non lo controllo più tardi.", false},
		{"Non ti avviso quando finisce.", false},
		// Spanish
		{"No te preocupes, revisaré más tarde.", true},
		{"No hay problema, reintentaré en unos minutos.", true},
		{"Aún no termina, revisaré más tarde.", true},
		{"No revisaré más tarde.", false},
		{"Nunca voy a monitorear el trabajo.", false},
		{"No te aviso cuando termine.", false},
		// French
		{"Ne t'inquiète pas, je vérifierai plus tard.", true},
		{"Pas de souci, je réessaierai dans quelques minutes.", true},
		{"Ce n'est pas fini, je vérifierai plus tard.", true},
		{"Je ne vérifierai pas plus tard.", false},
		{"Je ne vais pas surveiller le job.", false},
		{"Je vérifierai pas plus tard.", false},
		// German
		{"Keine Sorge, ich prüfe das später.", true},
		{"Mach dir keine Sorgen, ich schaue gleich nach.", true},
		{"Es ist noch nicht fertig, ich prüfe das später.", true},
		{"Ich werde es nicht später prüfen.", false},
		{"Ich prüfe das später nicht.", false},
		{"Ich werde den Deploy nicht beobachten.", false},
	})
}
