import { zodResolver } from "@hookform/resolvers/zod";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useForm, type Resolver } from "react-hook-form";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { z } from "zod";

import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from "../components/settings-form-layout";
import { SettingsPageFormActions } from "../components/settings-page-context";
import { SettingsSection } from "../components/settings-section";
import { useUpdateOption } from "../hooks/use-update-option";

const schema = z.object({
  enabled: z.boolean(),
  host: z.string(),
  port: z.string(),
  database: z.string(),
  user: z.string(),
  password: z.string(),
  sslMode: z.string(),
  storeFiles: z.boolean(),
  fileRoot: z.string(),
  maxFileMb: z.coerce.number().int().min(1),
  autoMessagePatterns: z.string(),
  automationModels: z.string(),
  memoryEnabled: z.boolean(),
  memoryBaseUrl: z.string(),
  memoryApiKey: z.string(),
  memoryWorkspace: z.string(),
  memoryPeerTemplate: z.string(),
  memoryAssistantPeer: z.string(),
  memorySessionMode: z.enum(["person", "conversation"]),
  memoryUserObserveMe: z.boolean(),
  memoryUserObserveOthers: z.boolean(),
  memoryAiObserveMe: z.boolean(),
  memoryAiObserveOthers: z.boolean(),
  memoryMinChars: z.coerce.number().int().min(1),
  memoryMaxChars: z.coerce.number().int().min(1),
  queueSize: z.coerce.number().int().min(1),
  workers: z.coerce.number().int().min(1),
  maxContentChars: z.coerce.number().int().min(1),
  maxQueuedMb: z.coerce.number().int().min(1),
  memoryMaxQueuedMb: z.coerce.number().int().min(1),
  fileRetentionDays: z.coerce.number().int().min(0),
  recordRetentionDays: z.coerce.number().int().min(0),
});

type Values = z.infer<typeof schema>;

type ChatRecordStatus = {
  running: boolean;
  queued: number;
  capacity: number;
  written?: number;
  dropped?: number;
  failed?: number;
  files?: number;
  queued_bytes?: number;
  memory_key_configured?: boolean;
  memory?: {
    running: boolean;
    queued: number;
    capacity: number;
    sent?: number;
    dropped?: number;
    failed?: number;
  };
  connection: string;
  dsn_configured: boolean;
  password_set: boolean;
  file_root: string;
};

export type ChatRecordDefaults = Values;

const SSL_MODES = ["disable", "require", "verify-ca", "verify-full"] as const;

// The templates decide who a memory belongs to, which is the one thing about
// this feature that cannot be guessed from the outside: a staff number
// identifies a person in one company, a key name in another. The presets are
// there so the common answers need no documentation; the field stays editable
// because the uncommon ones exist too.
function PeerPreset({
  options,
  onPick,
}: {
  options: { value: string; label: string }[];
  onPick: (value: string) => void;
}) {
  return (
    <Select onValueChange={(value) => onPick(String(value))}>
      <SelectTrigger className="w-full sm:w-56">
        <SelectValue placeholder="选择常用取值…" />
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value}>
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

export function ChatRecordSection({
  defaultValues,
}: {
  defaultValues: ChatRecordDefaults;
}) {
  const { t } = useTranslation();
  const updateOption = useUpdateOption();
  const [initializing, setInitializing] = useState(false);
  const [testing, setTesting] = useState(false);

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues,
  });

  // Only changed settings are sent, so the comparison needs to be against what
  // was last saved — not against the props this component first mounted with.
  // Otherwise changing a value, saving, and changing it back would compare
  // equal to the stale baseline and quietly send nothing.
  const [saved, setSaved] = useState<Values>(defaultValues);

  const { isDirty, isSubmitting } = form.formState;

  // The saved password never leaves the gateway, so the page shows a
  // description of the connection instead and treats an empty password box as
  // "keep the one you already have".
  const { data: status, refetch: refetchStatus } = useQuery({
    queryKey: ["chat-record-status"],
    queryFn: async () => {
      const { data } = await api.get<{ data: ChatRecordStatus }>(
        "/api/option/chat_record/status",
      );
      return data.data;
    },
    refetchInterval: 15_000,
  });

  // Connection details are sent with the request so an address can be checked
  // before it is saved.
  async function callWithConnection(
    path: string,
    setBusy: (busy: boolean) => void,
  ) {
    setBusy(true);
    try {
      const values = form.getValues();
      const { data } = await api.post<{ success: boolean; message: string }>(
        path,
        {
          host: values.host,
          port: values.port,
          database: values.database,
          user: values.user,
          password: values.password,
          ssl_mode: values.sslMode,
        },
      );
      if (data?.success) {
        toast.success(data.message);
        void refetchStatus();
      } else toast.error(data?.message ?? t("Request failed"));
    } catch (error) {
      toast.error(String(error));
    } finally {
      setBusy(false);
    }
  }

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = [];
    const push = (key: string, next: unknown, previous: unknown) => {
      if (next !== previous) updates.push({ key, value: String(next) });
    };

    push("chat_record_setting.enabled", values.enabled, saved.enabled);
    push("chat_record_setting.host", values.host, saved.host);
    push("chat_record_setting.port", values.port, saved.port);
    push("chat_record_setting.database", values.database, saved.database);
    push("chat_record_setting.user", values.user, saved.user);
    push("chat_record_setting.ssl_mode", values.sslMode, saved.sslMode);
    // An empty box means "leave the saved password alone".
    if (values.password !== "") {
      updates.push({
        key: "chat_record_setting.password",
        value: values.password,
      });
    }
    push(
      "chat_record_setting.store_files",
      values.storeFiles,
      saved.storeFiles,
    );
    push("chat_record_setting.file_root", values.fileRoot, saved.fileRoot);
    push(
      "chat_record_setting.auto_message_patterns",
      values.autoMessagePatterns,
      saved.autoMessagePatterns,
    );
    push(
      "chat_record_setting.automation_models",
      values.automationModels,
      saved.automationModels,
    );
    push(
      "chat_record_setting.memory_enabled",
      values.memoryEnabled,
      saved.memoryEnabled,
    );
    push(
      "chat_record_setting.memory_base_url",
      values.memoryBaseUrl,
      saved.memoryBaseUrl,
    );
    // Blank means "leave the stored key alone", the same as the password.
    if (values.memoryApiKey !== "") {
      updates.push({
        key: "chat_record_setting.memory_api_key",
        value: values.memoryApiKey,
      });
    }
    push(
      "chat_record_setting.memory_workspace",
      values.memoryWorkspace,
      saved.memoryWorkspace,
    );
    push(
      "chat_record_setting.memory_peer_template",
      values.memoryPeerTemplate,
      saved.memoryPeerTemplate,
    );
    push(
      "chat_record_setting.memory_assistant_peer",
      values.memoryAssistantPeer,
      saved.memoryAssistantPeer,
    );
    push(
      "chat_record_setting.memory_session_mode",
      values.memorySessionMode,
      saved.memorySessionMode,
    );
    push(
      "chat_record_setting.memory_user_observe_me",
      values.memoryUserObserveMe,
      saved.memoryUserObserveMe,
    );
    push(
      "chat_record_setting.memory_user_observe_others",
      values.memoryUserObserveOthers,
      saved.memoryUserObserveOthers,
    );
    push(
      "chat_record_setting.memory_ai_observe_me",
      values.memoryAiObserveMe,
      saved.memoryAiObserveMe,
    );
    push(
      "chat_record_setting.memory_ai_observe_others",
      values.memoryAiObserveOthers,
      saved.memoryAiObserveOthers,
    );
    push(
      "chat_record_setting.memory_min_chars",
      values.memoryMinChars,
      saved.memoryMinChars,
    );
    push(
      "chat_record_setting.memory_max_chars",
      values.memoryMaxChars,
      saved.memoryMaxChars,
    );
    if (values.maxFileMb !== saved.maxFileMb) {
      updates.push({
        key: "chat_record_setting.max_file_bytes",
        value: String(values.maxFileMb * 1024 * 1024),
      });
    }
    push("chat_record_setting.queue_size", values.queueSize, saved.queueSize);
    push("chat_record_setting.workers", values.workers, saved.workers);
    push(
      "chat_record_setting.max_content_chars",
      values.maxContentChars,
      saved.maxContentChars,
    );
    push(
      "chat_record_setting.file_retention_days",
      values.fileRetentionDays,
      saved.fileRetentionDays,
    );
    push(
      "chat_record_setting.record_retention_days",
      values.recordRetentionDays,
      saved.recordRetentionDays,
    );
    if (values.memoryMaxQueuedMb !== saved.memoryMaxQueuedMb) {
      updates.push({
        key: "chat_record_setting.memory_max_queued_bytes",
        value: String(values.memoryMaxQueuedMb * 1024 * 1024),
      });
    }
    if (values.maxQueuedMb !== saved.maxQueuedMb) {
      updates.push({
        key: "chat_record_setting.max_queued_bytes",
        value: String(values.maxQueuedMb * 1024 * 1024),
      });
    }

    if (updates.length === 0) {
      toast.info(t("No changes to save"));
      return;
    }
    for (const update of updates) {
      await updateOption.mutateAsync(update);
    }
    const nowSaved = { ...values, password: "" };
    setSaved(nowSaved);
    form.reset(nowSaved);
    void refetchStatus();
  }

  return (
    <SettingsSection title={t("Chat transcript recording")}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete="off">
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel={t("Save transcript settings")}
          />

          <FormField
            control={form.control}
            name="enabled"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t("Record chat transcripts")}</FormLabel>
                  <FormDescription>
                    {t(
                      "Stores each turn’s user message and model reply in a separate database. Writing happens on a queue the relay never waits on: if the store falls behind, transcripts are dropped rather than requests being slowed.",
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <div className="space-y-4 rounded-lg border p-4">
            <div>
              <h4 className="text-sm font-medium">{t("Database")}</h4>
              <p className="text-muted-foreground text-sm">
                {status?.dsn_configured
                  ? t("Connected to {{connection}}", {
                      connection: status.connection,
                    })
                  : t(
                      "A PostgreSQL database of your own. Fill this in, then press Initialise once to create the tables.",
                    )}
              </p>
            </div>

            <div className="grid gap-4 sm:grid-cols-2">
              <FormField
                control={form.control}
                name="host"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t("Host")}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="192.168.1.10" />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="port"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t("Port")}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="5432" />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="database"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t("Database name")}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="chatlog" />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="sslMode"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t("SSL mode")}</FormLabel>
                    <Select value={field.value} onValueChange={field.onChange}>
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        {SSL_MODES.map((mode) => (
                          <SelectItem key={mode} value={mode}>
                            {mode}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="user"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t("Username")}</FormLabel>
                    <FormControl>
                      <Input {...field} autoComplete="off" />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="password"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t("Password")}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type="password"
                        autoComplete="new-password"
                        placeholder={
                          status?.password_set
                            ? t("Saved — leave empty to keep it")
                            : ""
                        }
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                disabled={testing}
                onClick={() =>
                  callWithConnection("/api/option/chat_record/test", setTesting)
                }
              >
                {testing ? t("Testing…") : t("Test connection")}
              </Button>
              <Button
                type="button"
                disabled={initializing}
                onClick={() =>
                  callWithConnection(
                    "/api/option/chat_record/init",
                    setInitializing,
                  )
                }
              >
                {initializing ? t("Initialising…") : t("Initialise database")}
              </Button>
            </div>
          </div>

          {status ? (
            <p className="text-muted-foreground text-sm">
              {status.running
                ? t(
                    "Writer running · {{queued}}/{{capacity}} queued · {{heldMb}} MB held · {{written}} written · {{files}} files · {{dropped}} dropped · {{failed}} failed",
                    {
                      queued: status.queued,
                      capacity: status.capacity,
                      heldMb: (
                        (status.queued_bytes ?? 0) /
                        (1024 * 1024)
                      ).toFixed(1),
                      written: status.written ?? 0,
                      files: status.files ?? 0,
                      dropped: status.dropped ?? 0,
                      failed: status.failed ?? 0,
                    },
                  )
                : t("Writer stopped")}
            </p>
          ) : null}

          <FormField
            control={form.control}
            name="storeFiles"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t("Keep attached files and images")}</FormLabel>
                  <FormDescription>
                    {t(
                      "Attachments are written to disk in a folder per staff ID; the database keeps only the path. Files sent as links are noted but not downloaded.",
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name="fileRoot"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Attachment folder")}</FormLabel>
                <FormControl>
                  <Input {...field} placeholder="data/chat-record-files" />
                </FormControl>
                <FormDescription>
                  {t(
                    "Relative paths are resolved against the gateway’s working directory. Attachments are filed as <folder>/<staff ID>/<date>/.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="maxFileMb"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Largest attachment kept (MB)")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="autoMessagePatterns"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Machine-message markers")}</FormLabel>
                <FormControl>
                  <Textarea rows={5} className="font-mono text-sm" {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "One per line. A message containing any of these is filed as sent by a program rather than typed by a person. Bracketed tags, XML envelopes and system prompts sent as user turns are recognised without help — this is for your own prompt templates, which read like ordinary instructions.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="automationModels"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Background-only models")}</FormLabel>
                <FormControl>
                  <Textarea rows={4} className="font-mono text-sm" {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "One model name per line. Traffic on these is never a person talking, whatever the words say — summarisers, approval reviewers and title generators replay the person’s own text verbatim, so no text rule can tell them apart.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="memoryEnabled"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t("Feed a memory store")}</FormLabel>
                  <FormDescription>
                    {t(
                      "Sends each turn on to a honcho memory, filed under the key’s staff ID. Only turns a client positively declared to be a person speaking are sent — a transcript can carry a misfiled line and still be read, but a memory turns one into a lasting false fact about someone. Delivery has its own queue: a slow memory store loses memories, never transcripts.",
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <div className="grid gap-4 sm:grid-cols-2">
            <FormField
              control={form.control}
              name="memoryBaseUrl"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t("Memory service address")}</FormLabel>
                  <FormControl>
                    <Input {...field} placeholder="http://192.168.18.46:8000" />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="memoryApiKey"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t("Memory API key")}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type="password"
                      autoComplete="new-password"
                      placeholder={
                        status?.memory_key_configured
                          ? t("Saved — leave empty to keep it")
                          : ""
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="memoryWorkspace"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t("Workspace")}</FormLabel>
                  <FormControl>
                    <Input {...field} placeholder="yxsy" />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="memoryPeerTemplate"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>peerName</FormLabel>
                  <FormControl>
                    <div className="flex flex-col gap-2 sm:flex-row">
                      <Input {...field} placeholder="{staff_id}" />
                      <PeerPreset
                        onPick={field.onChange}
                        options={[
                          { value: "{staff_id}", label: t("Staff ID") },
                          { value: "{token_name}", label: t("Key name") },
                          { value: "{user_id}", label: t("User ID") },
                          {
                            value: "{staff_id}-{token_name}",
                            label: t("Staff ID + key name"),
                          },
                        ]}
                      />
                    </div>
                  </FormControl>
                  <FormDescription>
                    {t(
                      "Whose memory this is. Available: {staff_id}, {token_name}, {user_id}, {agent}, {model}.",
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="memoryAssistantPeer"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>aiPeer</FormLabel>
                  <FormControl>
                    <div className="flex flex-col gap-2 sm:flex-row">
                      <Input {...field} placeholder="{agent}-{staff_id}" />
                      <PeerPreset
                        onPick={field.onChange}
                        options={[
                          {
                            value: "{agent}-{staff_id}",
                            label: t("Agent, per person"),
                          },
                          { value: "{agent}", label: t("Agent, shared") },
                          {
                            value: "newapi-{staff_id}",
                            label: t("Fixed name, per person"),
                          },
                        ]}
                      />
                    </div>
                  </FormControl>
                  <FormDescription>
                    {t(
                      "Where the model’s replies go, so they read as context rather than facts about the person. {agent} is the agent the client named — Codex reports its subagents, Hermes reports nothing and falls back to “assistant”. Keeping {staff_id} in it stops one shared peer collecting a representation inside every person’s session.",
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name="memoryUserObserveMe"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t("Build a picture of the person")}</FormLabel>
                  <FormDescription>
                    {t(
                      "Lets the store read what someone says and keep what it learns about them. This is the memory itself — with it off, turns are still filed but nothing is ever concluded from them.",
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name="memoryUserObserveOthers"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t("Let the person observe the assistant")}</FormLabel>
                  <FormDescription>
                    {t(
                      "Keeps a picture of the assistant as seen by the person. Rarely wanted: it costs an inference per turn to describe something that is not a person.",
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name="memoryAiObserveMe"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t("Build a picture of the assistant")}</FormLabel>
                  <FormDescription>
                    {t(
                      "Keeps a picture of the assistant from its own replies. Off by default — an inference per reply, describing something with no memory worth keeping.",
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name="memoryAiObserveOthers"
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t("Let the assistant observe the person")}</FormLabel>
                  <FormDescription>
                    {t(
                      "Keeps this assistant’s own view of the person, separate from the shared picture. What makes an agent remember someone its own way; costs an inference per turn.",
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name="memorySessionMode"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Memory session grouping")}</FormLabel>
                <FormControl>
                  <Select value={field.value} onValueChange={field.onChange}>
                    <SelectTrigger className="w-full sm:w-72">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="person">
                        {t("One running session per person")}
                      </SelectItem>
                      <SelectItem value="conversation">
                        {t("Follow the client’s conversations")}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </FormControl>
                <FormDescription>
                  {t(
                    "Per person lets the store build a picture across conversations; per conversation keeps them separate.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="memoryMinChars"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Shortest remark worth remembering")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="memoryMaxQueuedMb"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Memory ceiling for the memory queue (MB)")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "Turns waiting to be sent hold their text in memory. Past this, memories are dropped rather than allowed to grow into the gateway’s heap — the transcript is never affected.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="memoryMaxChars"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Longest remark the memory accepts")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "Anything longer is cut before it is sent. A memory store refuses an oversized message outright rather than keeping what fits, so leave this below its own limit — Honcho's is 25000.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="queueSize"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Queue size")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "How many finished turns may wait to be written. A full queue drops new ones so no request ever waits.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="workers"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Writer count")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="maxContentChars"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Characters kept per message")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "Longer messages are cut at this length. A turn replays the whole conversation, so without a cap one row could hold megabytes.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="maxQueuedMb"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Memory ceiling for the queue (MB)")}</FormLabel>
                <FormControl>
                  <Input type="number" min={1} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "Waiting turns hold the request and reply in memory. If the database stalls, this is what stops the queue from growing into the gateway’s heap — past it, transcripts are dropped.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="fileRetentionDays"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Keep attachments for (days)")}</FormLabel>
                <FormControl>
                  <Input type="number" min={0} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "0 keeps them forever. Attachments fill a disk far faster than the rows do: one conversation with a screenshot in it outweighs thousands of plain turns.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="recordRetentionDays"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("Keep transcripts for (days)")}</FormLabel>
                <FormControl>
                  <Input type="number" min={0} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    "0 keeps them forever. Expired turns are deleted a few hundred at a time, with their attachments removed from disk first.",
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  );
}
