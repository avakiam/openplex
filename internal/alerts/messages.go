package alerts

import "strings"

// texts are the e-mails, in the languages of the panel. Placeholders are
// {name}; every message ends with the panel's address when it is known.
var texts = map[string]map[string]string{
	"en": {
		"reminder":                    "Still happening: ",
		"backup_failed.subject":       "Backup failed on {server}",
		"backup_failed.body":          "The {trigger} backup of {scope} failed on {time}:\n\n{message}\n\nCheck Backups in the panel.",
		"site_down.subject":           "Website down: {site}",
		"site_down.body":              "{site} has not been running since {time}:\n\n{message}\n\nIts overview in the panel shows the status and the logs.",
		"site_down.resolved":          "Website back up: {site}",
		"site_down.resolved_body":     "{site} is running again.",
		"database_full.subject":       "Database over its size limit: {database}",
		"database_full.body":          "{database} (account {owner}) went over its size limit of {limit}, so it is read-only for its applications now: they can read it but not save anything. Delete data from it, or raise its limit under Databases in the panel.",
		"database_full.resolved":      "Database writable again: {database}",
		"database_full.resolved_body": "{database} is back under its size limit, and its applications can save again.",
		"disk_full.subject":           "Disk almost full on {server}: {pct}%",
		"disk_full.body":              "The disk holding OpenPlex's data is {pct}% full ({free} free). Websites, databases, mail and backups fail when it is full: delete old files or backups, or grow the disk.",
		"disk_full.resolved":          "Disk usage back to normal on {server}: {pct}%",
		"disk_full.resolved_body":     "The disk is {pct}% full now ({free} free).",
		"cert_expiring.subject":       "Certificate not renewing: {host}",
		"cert_expiring.body":          "The HTTPS certificate of {host} expires on {date} and has not been renewed yet. Usual causes: the name's DNS no longer points at this server, or port 80 is blocked from the internet. Visitors see a security warning once it expires.",
		"cert_expiring.resolved":      "Certificate renewed: {host}",
		"cert_expiring.resolved_body": "{host} has a new certificate, valid until {date}.",
		"update_available.subject":    "OpenPlex {version} is available",
		"update_available.body":       "A new stable version of OpenPlex is available ({current} is installed). Install it from Updates in the panel.\n\n{notes}",
		"update_failed.subject":       "OpenPlex update to {version} did not complete",
		"update_failed.body":          "The update from {from} to {version} did not complete ({result}), and {from} is running again:\n\n{message}",
		"test.subject":                "OpenPlex test alert from {server}",
		"test.body":                   "This is a test sent from the Alerts page of {server}. Alerts reach this address.",
		"panel":                       "Panel: {url}",
		"extension":                   "Sent by the {name} extension.",
		"scope.all":                   "all websites and databases",
		"trigger.scheduled":           "scheduled",
		"trigger.manual":              "manual",
	},
	"es": {
		"reminder":                    "Sigue pasando: ",
		"backup_failed.subject":       "Ha fallado una copia de seguridad en {server}",
		"backup_failed.body":          "La copia {trigger} de {scope} falló el {time}:\n\n{message}\n\nRevisa Copias de seguridad en el panel.",
		"site_down.subject":           "Web caída: {site}",
		"site_down.body":              "{site} no funciona desde el {time}:\n\n{message}\n\nSu resumen en el panel muestra el estado y los registros.",
		"site_down.resolved":          "Web recuperada: {site}",
		"site_down.resolved_body":     "{site} vuelve a funcionar.",
		"database_full.subject":       "Base de datos por encima de su límite: {database}",
		"database_full.body":          "{database} (cuenta {owner}) ha superado su límite de tamaño de {limit}, así que ahora es de solo lectura para sus aplicaciones: pueden leerla pero no guardar nada. Borra datos de ella o sube su límite en Bases de datos, en el panel.",
		"database_full.resolved":      "Base de datos de nuevo con escritura: {database}",
		"database_full.resolved_body": "{database} vuelve a estar por debajo de su límite y sus aplicaciones pueden guardar de nuevo.",
		"disk_full.subject":           "Disco casi lleno en {server}: {pct}%",
		"disk_full.body":              "El disco con los datos de OpenPlex está al {pct}% ({free} libres). Las webs, las bases de datos, el correo y las copias fallan cuando se llena: borra archivos o copias antiguas, o amplía el disco.",
		"disk_full.resolved":          "Uso de disco normal de nuevo en {server}: {pct}%",
		"disk_full.resolved_body":     "El disco está ahora al {pct}% ({free} libres).",
		"cert_expiring.subject":       "Certificado sin renovar: {host}",
		"cert_expiring.body":          "El certificado HTTPS de {host} caduca el {date} y todavía no se ha renovado. Causas habituales: el DNS del nombre ya no apunta a este servidor, o el puerto 80 está bloqueado desde internet. Cuando caduque, los visitantes verán un aviso de seguridad.",
		"cert_expiring.resolved":      "Certificado renovado: {host}",
		"cert_expiring.resolved_body": "{host} tiene un certificado nuevo, válido hasta el {date}.",
		"update_available.subject":    "OpenPlex {version} está disponible",
		"update_available.body":       "Hay una nueva versión estable de OpenPlex (tienes la {current}). Instálala desde Actualizaciones en el panel.\n\n{notes}",
		"update_failed.subject":       "La actualización de OpenPlex a la {version} no se completó",
		"update_failed.body":          "La actualización de la {from} a la {version} no se completó ({result}) y vuelve a funcionar la {from}:\n\n{message}",
		"test.subject":                "Alerta de prueba de OpenPlex desde {server}",
		"test.body":                   "Esto es una prueba enviada desde la página Alertas de {server}. Las alertas llegan a esta dirección.",
		"extension":                   "Enviado por la extensión {name}.",
		"panel":                       "Panel: {url}",
		"scope.all":                   "todas las webs y bases de datos",
		"trigger.scheduled":           "programada",
		"trigger.manual":              "manual",
	},
	"ca": {
		"reminder":                    "Continua passant: ",
		"backup_failed.subject":       "Ha fallat una còpia de seguretat a {server}",
		"backup_failed.body":          "La còpia {trigger} de {scope} va fallar el {time}:\n\n{message}\n\nRevisa Còpies de seguretat al tauler.",
		"site_down.subject":           "Web caigut: {site}",
		"site_down.body":              "{site} no funciona des del {time}:\n\n{message}\n\nEl seu resum al tauler mostra l'estat i els registres.",
		"site_down.resolved":          "Web recuperat: {site}",
		"site_down.resolved_body":     "{site} torna a funcionar.",
		"database_full.subject":       "Base de dades per sobre del seu límit: {database}",
		"database_full.body":          "{database} (compte {owner}) ha superat el seu límit de mida de {limit}, així que ara és de només lectura per a les seves aplicacions: la poden llegir però no hi poden desar res. Esborra'n dades o puja'n el límit a Bases de dades, al tauler.",
		"database_full.resolved":      "Base de dades de nou amb escriptura: {database}",
		"database_full.resolved_body": "{database} torna a estar per sota del seu límit i les seves aplicacions hi poden tornar a desar.",
		"disk_full.subject":           "Disc gairebé ple a {server}: {pct}%",
		"disk_full.body":              "El disc amb les dades d'OpenPlex és al {pct}% ({free} lliures). Els webs, les bases de dades, el correu i les còpies fallen quan s'omple: esborra fitxers o còpies antigues, o amplia el disc.",
		"disk_full.resolved":          "Ús de disc normal de nou a {server}: {pct}%",
		"disk_full.resolved_body":     "El disc és ara al {pct}% ({free} lliures).",
		"cert_expiring.subject":       "Certificat sense renovar: {host}",
		"cert_expiring.body":          "El certificat HTTPS de {host} caduca el {date} i encara no s'ha renovat. Causes habituals: el DNS del nom ja no apunta a aquest servidor, o el port 80 està bloquejat des d'internet. Quan caduqui, els visitants veuran un avís de seguretat.",
		"cert_expiring.resolved":      "Certificat renovat: {host}",
		"cert_expiring.resolved_body": "{host} té un certificat nou, vàlid fins al {date}.",
		"update_available.subject":    "OpenPlex {version} està disponible",
		"update_available.body":       "Hi ha una nova versió estable d'OpenPlex (tens la {current}). Instal·la-la des d'Actualitzacions al tauler.\n\n{notes}",
		"update_failed.subject":       "L'actualització d'OpenPlex a la {version} no es va completar",
		"update_failed.body":          "L'actualització de la {from} a la {version} no es va completar ({result}) i torna a funcionar la {from}:\n\n{message}",
		"test.subject":                "Alerta de prova d'OpenPlex des de {server}",
		"test.body":                   "Això és una prova enviada des de la pàgina Alertes de {server}. Les alertes arriben a aquesta adreça.",
		"extension":                   "Enviat per l'extensió {name}.",
		"panel":                       "Tauler: {url}",
		"scope.all":                   "tots els webs i bases de dades",
		"trigger.scheduled":           "programada",
		"trigger.manual":              "manual",
	},
}

// text returns the message key in lang (English if missing) with vars filled in.
func text(lang, key string, vars map[string]string) string {
	s, ok := texts[lang][key]
	if !ok {
		s = texts["en"][key]
	}
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}
