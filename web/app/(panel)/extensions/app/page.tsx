"use client";
import { useSearchParams } from "next/navigation";
import { Suspense } from "react";

import { ExtensionFrame } from "@/components/extension-frame";
import { ErrorBox, PageHeader, Spinner } from "@/components/ui";
import { useExtensionsUI } from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import { extText } from "@/lib/types";

function ExtensionApp() {
  const { t, locale } = useT();
  const params = useSearchParams();
  const id = params.get("id") ?? "";
  const pageId = params.get("page") ?? "";
  const { data, isLoading } = useExtensionsUI();
  const ext = data?.find((e) => e.id === id);
  const page = ext?.pages.find((p) => p.id === pageId);
  if (isLoading) return <Spinner />;
  if (!ext || !page) return <ErrorBox error={new Error(t("This extension page is not available."))} />;
  return (
    <>
      <PageHeader title={extText(page.title, locale)} description={extText(ext.name, locale)} />
      <ExtensionFrame ext={ext.id} page={page.id} />
    </>
  );
}

export default function ExtensionAppPage() {
  return (
    <Suspense fallback={<Spinner />}>
      <ExtensionApp />
    </Suspense>
  );
}
