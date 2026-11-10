// Translation catalogues. Keys are the English source strings; English needs
// entries only for plural forms. Each area file is owned by one part of the UI.
import caCommon from "./ca/common.json";
import caCron from "./ca/cron.json";
import caData from "./ca/data.json";
import caDomains from "./ca/domains.json";
import caSecurity from "./ca/security.json";
import caServer from "./ca/server.json";
import enCommon from "./en/common.json";
import enCron from "./en/cron.json";
import enData from "./en/data.json";
import enDomains from "./en/domains.json";
import enSecurity from "./en/security.json";
import enServer from "./en/server.json";
import esCommon from "./es/common.json";
import esCron from "./es/cron.json";
import esData from "./es/data.json";
import esDomains from "./es/domains.json";
import esSecurity from "./es/security.json";
import esServer from "./es/server.json";

export type Entry = string | { [form: string]: string };
export type Catalogue = Record<string, Entry>;

export const catalogues: Record<"en" | "es" | "ca", Catalogue> = {
  en: { ...enServer, ...enData, ...enDomains, ...enCron, ...enSecurity, ...enCommon },
  es: { ...esServer, ...esData, ...esDomains, ...esCron, ...esSecurity, ...esCommon },
  ca: { ...caServer, ...caData, ...caDomains, ...caCron, ...caSecurity, ...caCommon },
};
