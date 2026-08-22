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
  queueSize: z.coerce.number().int().min(1),
  workers: z.coerce.number().int().min(1),
  maxContentChars: z.coerce.number().int().min(1),
  maxQueuedMb: z.coerce.number().int().min(1),
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
  connection: string;
  dsn_configured: boolean;
  password_set: boolean;
  file_root: string;
};

export type ChatRecordDefaults = Values;

const SSL_MODES = ["disable", "require", "verify-ca", "verify-full"] as const;

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
        </SettingsForm>
      </Form>
    </SettingsSection>
  );
}
