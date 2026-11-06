package promise

import "regexp"

var spanish = &language{
	code: "es",
	skip: regexp.MustCompile(`\b(?:quieres|quiere|quieren|prefieres|prefiere|puedo|podria|podre|puedes|no|nunca|sin|nadie|ningun\w*)\b` +
		`|\bsi (?:quieres|quiere|prefieres|prefiere|lo deseas|lo desea|necesitas|necesita|deseas)\b|\bme gustaria\b|\b(?:avisame|dime) si\b`),
	wait: re(
		`\b(?:esperare|aguardare|voy a esperar|estoy esperando|espero a que|aguardo|me quedo esperando)\b`,
	),
	userWait: regexp.MustCompile(`^\W*(?:a que (?:me|nos|tu|tus|usted|confirm\w*|respond\w*|dig\w*|dec\w*|apru\w*|indiqu\w*|de[sn]?)\b|` +
		`(?:tu|tus|t[uú])\b|su (?:confirm\w*|respuesta|aprob\w*|ok|visto|feedback)|` +
		`(?:la |el |las |una |un )?(?:confirmaci\w*|respuesta|aprobaci\w*|visto bueno|indicaci\w*|feedback|luz verde|ok)\b)`),
	strong: re(
		`\b(?:monitoreare|monitorizare|monitoreo|vigilare|estoy (?:monitoreando|monitorizando|vigilando|pendiente)|voy a (?:monitorear|monitorizar|vigilar|supervisar|seguir)|` +
			`estare (?:pendiente|atento|atenta|vigilando|monitoreando|revisando|al tanto)|seguire (?:vigilando|monitoreando|revisando|pendiente)|sigo (?:vigilando|pendiente|monitoreando)|` +
			`mantendre (?:un ojo|vigilado|monitoreado)|tendre (?:un ojo|pendiente)|` +
			`volvere a (?:revisar|intentar|comprobar|verificar|mirar)|vuelvo a (?:revisar|intentar|comprobar|verificar|mirar)|reintentare|` +
			`te (?:aviso|avisare|informo|informare|escribo|escribire|digo|dire) (?:cuando|en cuanto|apenas|una vez|tan pronto|luego|mas tarde)|te mantendre (?:al tanto|informado|informada))\b`,
	),
	weak: re(`\b(?:revisare|reviso|comprobare|compruebo|verificare|verifico|mirare|miro|movere|muevo|reiniciare|reinicio|relanzare|relanzo|continuare|continuo|procedere|procedo|probare|pruebo|intentare|intento|actualizare|actualizo|ejecutare|ejecuto|chequeare|chequeo|vere|` +
		`voy a (?:revisar|comprobar|verificar|mirar|mover|reiniciar|relanzar|continuar|proceder|probar|intentar|actualizar|ejecutar|chequear))\b`),
	time: regexp.MustCompile(`(?:\b(?:mas tarde|en breve|dentro de poco|en un rato|en un momento|manana|esta noche|esta tarde|mas adelante|` +
		`(?:en|dentro de) (?:unos |unas |\d+ |un par de |cinco |diez |quince |veinte |treinta |un )?(?:segundos?|minutos?|horas?)|` +
		`(?:cuando|en cuanto|una vez que|tan pronto como|apenas|despues de que|hasta que)\b.{0,50}?\b(?:termin\w+|acab\w+|finaliz\w+|complet\w+|list[oa]s?|hecho|conclu\w+|llegu\w+|falle\w*|pase\w*))\b|\bluego$)`),
}
