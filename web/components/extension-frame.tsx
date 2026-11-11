"use client";
import { useEffect, useState } from "react";

import { ErrorBox, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { useT } from "@/lib/i18n";

/**
 * Shows an extension page. The page is served by the extension through the
 * panel and runs sandboxed (no same-origin): it can't reach the panel's
 * session. Opening it creates a page session for the signed-in user.
 */
export function ExtensionFrame({ ext, page, siteTab, domainId }: { ext: string; page?: string; siteTab?: string; domainId?: number }) {
  const { t, locale } = useT();
  const [url, setUrl] = useState("");
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    let live = true;
    setUrl("");
    setError(null);
    api
      .post<{ url: string }>(`/extensions/${encodeURIComponent(ext)}/session`, siteTab ? { site_tab: siteTab, domain_id: domainId } : { page })
      .then((r) => {
        if (!live) return;
        const theme = document.documentElement.classList.contains("dark") ? "dark" : "light";
        setUrl(`${r.url}?lang=${locale}&theme=${theme}`);
      })
      .catch((e: Error) => live && setError(e));
    return () => {
      live = false;
    };
  }, [ext, page, siteTab, domainId, locale]);

  if (error) return <ErrorBox error={error} />;
  if (!url)
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  return (
    <iframe
      key={url}
      src={url}
      title={t("Extension")}
      sandbox="allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads allow-modals"
      referrerPolicy="no-referrer"
      className="h-[calc(100dvh-14rem)] min-h-[32rem] w-full rounded-lg border bg-card"
    />
  );
}
