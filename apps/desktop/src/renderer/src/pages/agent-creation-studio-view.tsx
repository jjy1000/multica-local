import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { FlaskConical, Plus } from "lucide-react";
import { api, ApiError } from "@multica/core/api";
import { useT } from "@multica/views/i18n";
import { AppLink } from "@multica/views/navigation";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useQuery } from "@tanstack/react-query";
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "@multica/ui/components/ui/tabs";

// AgentCreationStudioView (0.3.45) — action-type lab distinct
// from the 8 existing visibility-gated or issue-bound flag
// views. The user navigates INTO this studio from the issue
// picker's LabPicker.onAction footer and drafts one of three
// resource kinds (agent / skill / squad), then submits through
// the existing REST endpoints directly via `api.createAgent /
// createSkill / createSquad` (no new mutation hook, no new IPC,
// no new sqlc).
//
// 0.3.45 CONTOUR:
//   - Distinct from agent_self_optimization / constitution_agent
//     (which are visibility gates that hide / reveal agents but
//     don't author any from the UI).
//   - Distinct from Claude Lab tabs (which are issue-bound panel
//     surfaces that operate on already-existing resource rows).
//   - Distinct from the regular Create-Agent dialog under
//     packages/views/agents/components/create-agent-dialog.tsx
//     (which only authors a single agent type). The studio is
//     the unified multi-resource entry point; existing single-
//     resource dialogs stay intact for users arriving via those
//     sub-routes.
//
// 0.3.45 hard constraint:
//   - No migration / sqlc regen / new IPC channel. Everything goes
//     through existing api methods.
//   - Flag declaration in catalog.go is `DefaultVal: false` and we
//     deliberately do NOT gate this view on flag enablement — the
//     LabPicker.onAction UX fires whether or not Labs sets the
//     flag on. (Future: we may flip to render-gated if the
//     `agent_creation_studio` flag stays a "discovery" toggle.)
//   - The "与智能体宪法兼容" checkbox is a UI placeholder only;
//     `CreateAgentRequest` does not yet accept a system_key field
//     (would require the upstream `agent.system_key` column that
//     this fork has not yet migrated). Release notes flag it as
//     deferred to 0.3.45.1.

type StudioTab = "agent" | "skill" | "squad";

export function AgentCreationStudioView() {
  const { t } = useT("experimental");
  const [searchParams] = useSearchParams();
  const fromIssueId = searchParams.get("from_issue");
  const [tab, setTab] = useState<StudioTab>("agent");

  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header />
      <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
        <Intro />
        {fromIssueId && <BackToIssueBanner issueId={fromIssueId} />}
        <Tabs
          value={tab}
          onValueChange={(v) => setTab(v as StudioTab)}
          className="flex flex-col gap-6"
        >
          <TabsList className="grid w-full grid-cols-3">
            <TabsTrigger value="agent">
              {t(($) => $.agent_creation_studio_view.tab_agent) ?? "智能体"}
            </TabsTrigger>
            <TabsTrigger value="skill">
              {t(($) => $.agent_creation_studio_view.tab_skill) ?? "技能"}
            </TabsTrigger>
            <TabsTrigger value="squad">
              {t(($) => $.agent_creation_studio_view.tab_squad) ?? "团队"}
            </TabsTrigger>
          </TabsList>
          <TabsContent value="agent">
            <CreateAgentForm />
          </TabsContent>
          <TabsContent value="skill">
            <CreateSkillForm />
          </TabsContent>
          <TabsContent value="squad">
            <CreateSquadForm />
          </TabsContent>
        </Tabs>
      </main>
    </div>
  );
}

function Header() {
  return (
    <header className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-background px-6 text-xs text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">试验性功能</span>
        <span className="text-muted-foreground/60">/</span>
        <span>智能体创建</span>
      </div>
    </header>
  );
}

function Intro() {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">
        智能体创建
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        在此处创建智能体、技能或团队资源。创建结果立即出现在主产品的常规列表里,
        任何 agent 都可立即引用。
      </p>
    </section>
  );
}

function BackToIssueBanner({ issueId }: { issueId: string }) {
  return (
    <section className="rounded-md border border-border bg-muted/40 px-4 py-2 text-xs">
      <span className="text-muted-foreground">从问题面板拉起 · </span>
      <AppLink
        href={`/issues/${issueId}`}
        className="font-medium text-purple-700 hover:underline dark:text-purple-300"
      >
        返回问题《{issueId.slice(0, 8)}》
      </AppLink>
    </section>
  );
}

// ---------------------------------------------------------------------------
// 3 tab bodies — shared form scaffolding, each maps its inputs into the
// corresponding api method. The "与智能体宪法兼容" checkbox is rendered on
// every form as a placeholder for 0.3.45.1 system_key wiring.
// ---------------------------------------------------------------------------

interface CompatToggleProps {
  label: string;
  checked: boolean;
  onChange: (next: boolean) => void;
  hint?: string;
}

function CompatToggle({ label, checked, onChange, hint }: CompatToggleProps) {
  return (
    <label className="flex items-start gap-2 text-xs text-muted-foreground">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5"
      />
      <span>
        <span className="font-medium text-foreground">{label}</span>
        {hint && <span className="ml-1">— {hint}</span>}
      </span>
    </label>
  );
}

interface ResourceFormContext {
  defaultRuntimeId: string | null;
}

interface ResourceFormProps {
  fieldNameLabel: string;
  fieldDescriptionLabel: string;
  namePlaceholder: string;
  descriptionPlaceholder: string;
  compatLabel: string;
  compatHint: string;
  submitLabel: string;
  onSubmit: (
    input: { name: string; description: string },
    ctx: ResourceFormContext,
  ) => Promise<void>;
}

function ResourceForm({
  fieldNameLabel,
  fieldDescriptionLabel,
  namePlaceholder,
  descriptionPlaceholder,
  compatLabel,
  compatHint,
  submitLabel,
  onSubmit,
}: ResourceFormProps) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [compat, setCompat] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Workspace runtimes — `createAgent` requires `runtime_id`. We
  // fetch the workspace runtimes once via the same query key the
  // agents page uses; the first runtime becomes the default. The
  // 0.3.45.1 follow-up surfaces a runtime picker UI; for now the
  // user has no way to override, which matches the "drafts a
  // resource quickly" UX of this studio.
  const { data: runtimesData } = useQuery({
    queryKey: ["runtimes", "list"],
    queryFn: () => api.listRuntimes(),
  });
  const defaultRuntimeId = useMemo(() => {
    const rt = (runtimesData ?? [])[0];
    return rt ? rt.id : null;
  }, [runtimesData]);

  const canSubmit = useMemo(
    () => name.trim().length > 0 && !submitting && !!defaultRuntimeId,
    [name, submitting, defaultRuntimeId],
  );

  return (
    <form
      className="flex flex-col gap-4 rounded-xl border border-border bg-card p-6"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!canSubmit || !defaultRuntimeId) return;
        setSubmitting(true);
        setError(null);
        try {
          await onSubmit(
            { name: name.trim(), description: description.trim() },
            { defaultRuntimeId },
          );
          // success → onSubmit routed to a detail page; this line
          // only fires on slow / failed submit where the parent
          // didn't navigate.
        } catch (err) {
          if (err instanceof ApiError) {
            setError(err.message);
          } else if (err instanceof Error) {
            setError(err.message);
          } else {
            setError(String(err));
          }
          setSubmitting(false);
        }
      }}
    >
      <div>
        <label className="block text-xs font-medium text-foreground">
          {fieldNameLabel}
        </label>
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={namePlaceholder}
          className="mt-1"
        />
      </div>
      <div>
        <label className="block text-xs font-medium text-foreground">
          {fieldDescriptionLabel}
        </label>
        <Input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder={descriptionPlaceholder}
          className="mt-1"
        />
      </div>
      <CompatToggle
        label={compatLabel}
        checked={compat}
        onChange={setCompat}
        hint={compatHint}
      />
      {error && <p className="text-xs text-destructive">{error}</p>}
      <div className="flex justify-end">
        <Button type="submit" disabled={!canSubmit}>
          <Plus className="size-3.5" />
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}

function CreateAgentForm() {
  const { t } = useT("experimental");
  return (
    <ResourceForm
      fieldNameLabel={
        t(($) => $.agent_creation_studio_view.field_name) ?? "名称"
      }
      fieldDescriptionLabel={
        t(($) => $.agent_creation_studio_view.field_description) ?? "描述"
      }
      namePlaceholder="例: 财报分析"
      descriptionPlaceholder="一句话职责"
      compatLabel={
        t(($) => $.agent_creation_studio_view.compat_label) ??
        "与智能体宪法兼容"
      }
      compatHint={
        t(($) => $.agent_creation_studio_view.compat_hint) ??
        "0.3.45 UI 占位 — 0.3.45.1 接入 system_key 字段"
      }
      submitLabel={
        t(($) => $.agent_creation_studio_view.submit_create) ?? "创建"
      }
      onSubmit={async ({ name, description }, ctx) => {
        const agent = await api.createAgent({
          name,
          description,
          runtime_id: ctx.defaultRuntimeId!,
          // system_key wiring deferred to 0.3.45.1 (see file header).
        });
        window.location.assign(`/agents/${agent.id}`);
      }}
    />
  );
}

function CreateSkillForm() {
  const { t } = useT("experimental");
  return (
    <ResourceForm
      fieldNameLabel={
        t(($) => $.agent_creation_studio_view.field_name) ?? "名称"
      }
      fieldDescriptionLabel={
        t(($) => $.agent_creation_studio_view.field_description) ?? "描述"
      }
      namePlaceholder="例: OCR 发票"
      descriptionPlaceholder="一句话功能"
      compatLabel={
        t(($) => $.agent_creation_studio_view.compat_label) ??
        "与智能体宪法兼容"
      }
      compatHint={
        t(($) => $.agent_creation_studio_view.compat_hint) ??
        "0.3.45 UI 占位"
      }
      submitLabel={
        t(($) => $.agent_creation_studio_view.submit_create) ?? "创建"
      }
      onSubmit={async ({ name, description }) => {
        const skill = await api.createSkill({
          name,
          description,
        });
        window.location.assign(`/skills/${skill.id}`);
      }}
    />
  );
}

function CreateSquadForm() {
  const { t } = useT("experimental");
  return (
    <ResourceForm
      fieldNameLabel={
        t(($) => $.agent_creation_studio_view.field_name) ?? "名称"
      }
      fieldDescriptionLabel={
        t(($) => $.agent_creation_studio_view.field_description) ?? "描述"
      }
      namePlaceholder="例: 财报三件套"
      descriptionPlaceholder="一句话目标"
      compatLabel={
        t(($) => $.agent_creation_studio_view.compat_label) ??
        "与智能体宪法兼容"
      }
      compatHint={
        t(($) => $.agent_creation_studio_view.compat_hint) ??
        "0.3.45 UI 占位"
      }
      submitLabel={
        t(($) => $.agent_creation_studio_view.submit_create) ?? "创建"
      }
      onSubmit={async ({ name, description }) => {
        // squad requires a leader_id; the studio cannot author a
        // squad from scratch with no agents yet. UI: keep the squad
        // submit button visible but show a friendly error if the
        // server rejects it for missing leader. The 0.3.45.1 follow-up
        // wires a leader picker that lets the user pick an existing
        // agent as the squad leader.
        const squad = await api.createSquad({
          name,
          description,
          leader_id: "",
        });
        window.location.assign(`/squads/${squad.id}`);
      }}
    />
  );
}
