"use client";

import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Image as ImageIcon,
  BarChart3,
  Table as TableIcon,
  Code2,
  FileText,
  Type,
  FileCode,
  Upload,
  Loader2,
  PackageOpen,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@multica/ui/components/ui/dialog";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { ArtifactRenderer, type Artifact } from "./artifact-renderer";

// 0.3.60 Labs sandbox — artifact gallery for a user plugin.
//
// All network calls go through api.rawRequest (per the CLAUDE.md 0.3.30
// contract — never a bare fetch): rawRequest prefixes the configured API
// host and injects the Bearer/CSRF/workspace headers, which a site-relative
// fetch would silently drop in the packaged desktop renderer.
//
// v1: hardcoded Chinese labels; i18n keys deferred.

export interface ArtifactGalleryProps {
  pluginSlug: string;
}

const TYPE_ICON: Record<Artifact["type"], typeof ImageIcon> = {
  image: ImageIcon,
  chart: BarChart3,
  table: TableIcon,
  code: Code2,
  file: FileText,
  text: Type,
  html: FileCode,
};

const TYPE_LABEL: Record<Artifact["type"], string> = {
  image: "图片",
  chart: "图表",
  table: "表格",
  code: "代码",
  file: "文件",
  text: "文本",
  html: "网页",
};

function formatTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** One-line preview for the card body. */
function previewText(artifact: Artifact): string {
  switch (artifact.type) {
    case "code":
    case "text":
      return typeof artifact.data === "string"
        ? artifact.data.split("\n")[0]?.slice(0, 60) ?? ""
        : "";
    case "table":
      return "表格数据";
    case "chart":
      return "图表数据";
    case "file":
      return artifact.mime_type ?? "文件";
    default:
      return "";
  }
}

async function fetchArtifacts(slug: string): Promise<Artifact[]> {
  const res = await api.rawRequest(`/api/user-plugins/${encodeURIComponent(slug)}/artifacts`);
  if (!res.ok) {
    throw new Error(`加载产物失败: ${res.status}`);
  }
  const body = (await res.json()) as Artifact[] | { artifacts?: Artifact[] };
  return Array.isArray(body) ? body : (body.artifacts ?? []);
}

export function ArtifactGallery({ pluginSlug }: ArtifactGalleryProps) {
  const qc = useQueryClient();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const [selected, setSelected] = useState<Artifact | null>(null);

  const { data: artifacts, isLoading, isError } = useQuery({
    queryKey: ["user-plugin-artifacts", pluginSlug],
    queryFn: () => fetchArtifacts(pluginSlug),
    staleTime: 30_000,
  });

  const invalidate = () =>
    qc.invalidateQueries({ queryKey: ["user-plugin-artifacts", pluginSlug] });

  async function handleUpload(file: File) {
    setUploading(true);
    try {
      const form = new FormData();
      form.append("file", file);
      const res = await api.rawRequest(
        `/api/user-plugins/${encodeURIComponent(pluginSlug)}/artifacts`,
        { method: "POST", body: form },
      );
      if (!res.ok) {
        throw new Error(`上传失败: ${res.status}`);
      }
      toast.success("产物已上传");
      invalidate();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-foreground">产物</h3>
        <div>
          <input
            ref={fileInputRef}
            type="file"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) void handleUpload(file);
            }}
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={uploading}
            onClick={() => fileInputRef.current?.click()}
          >
            {uploading ? (
              <Loader2 className="h-3 w-3 animate-spin" aria-hidden />
            ) : (
              <Upload className="h-3 w-3" aria-hidden />
            )}
            上传产物
          </Button>
        </div>
      </div>

      {isLoading ? (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-28 w-full" />
          ))}
        </div>
      ) : isError ? (
        <p className="text-sm text-muted-foreground">加载产物失败，请稍后重试。</p>
      ) : !artifacts || artifacts.length === 0 ? (
        <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-border py-10 text-center">
          <PackageOpen className="h-6 w-6 text-muted-foreground" aria-hidden />
          <p className="text-sm text-muted-foreground">
            暂无产物 — 通过智能体或手动上传创建
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
          {artifacts.map((artifact) => {
            const Icon = TYPE_ICON[artifact.type] ?? FileText;
            const isImage = artifact.type === "image" && artifact.url;
            return (
              <button
                key={artifact.id}
                type="button"
                onClick={() => setSelected(artifact)}
                className="group flex flex-col overflow-hidden rounded-lg border border-border bg-background text-left transition-colors hover:border-foreground/30 hover:bg-muted/40"
              >
                <div className="flex h-24 items-center justify-center overflow-hidden border-b border-border bg-muted/30">
                  {isImage ? (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img
                      src={`${api.getBaseUrl()}${artifact.url}`}
                      alt={artifact.title}
                      className="h-full w-full object-cover"
                    />
                  ) : (
                    <Icon className="h-7 w-7 text-muted-foreground" aria-hidden />
                  )}
                </div>
                <div className="flex flex-1 flex-col gap-1 p-2.5">
                  <div className="flex items-center gap-1.5">
                    <span className="rounded border border-muted-foreground/30 bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                      {TYPE_LABEL[artifact.type] ?? artifact.type}
                    </span>
                  </div>
                  <p className="line-clamp-1 text-sm font-medium text-foreground">
                    {artifact.title}
                  </p>
                  <p className="line-clamp-1 text-xs text-muted-foreground">
                    {previewText(artifact) || formatTime(artifact.created_at)}
                  </p>
                  <p className="text-[10px] text-muted-foreground/70">
                    {formatTime(artifact.created_at)}
                  </p>
                </div>
              </button>
            );
          })}
        </div>
      )}

      <Dialog open={selected !== null} onOpenChange={(open) => !open && setSelected(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{selected?.title ?? "产物"}</DialogTitle>
            <DialogDescription>
              {selected ? `${TYPE_LABEL[selected.type] ?? selected.type} · ${formatTime(selected.created_at)}` : ""}
            </DialogDescription>
          </DialogHeader>
          {selected && (
            <div className="max-h-[70vh] overflow-y-auto">
              <ArtifactRenderer artifact={selected} pluginSlug={pluginSlug} />
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
