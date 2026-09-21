import { useState } from "react";
import { useTranslation } from "react-i18next";
import { createNode, provisionNode, regenNodeJoin, type NodeView } from "./api";
import { errMessage, notifyError, notifySuccess } from "./notify";
import { Button, cn, Code, Modal, PasswordInput, Textarea, TextInput } from "./ui";

// DialogTabs is the in-modal tab strip used by the server settings dialogs, so a
// server's many sections (domain / routing / DNS / …) don't stack into one long
// scroll. All tabs' state lives in the parent, so switching never loses edits and
// the single footer Save persists everything regardless of the active tab.
// ERR_PREFIX marks a failed line in the live install log so it can be coloured
// without parsing the message itself.
const ERR_PREFIX = "ERROR";

// InstallCommandModal shows the one-line install command exactly once after a node
// is created or its token is regenerated.
export function InstallCommandModal({
  command,
  onClose,
}: {
  command: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Modal open onClose={onClose} title={t("nodes.installCommand")} size="lg">
      <p className="text-sm text-ink-muted">
        {t("nodes.installCommandHint")}
      </p>
      <div className="mt-3">
        <Code block copy>
          {command}
        </Code>
      </div>
      <div className="mt-4 flex justify-end">
        <Button onClick={onClose}>{t("common.done")}</Button>
      </div>
    </Modal>
  );
}

// AddNodeDialog collects a name + host and creates the node, either handing back
// the copy-paste install command or (auto mode) installing it over SSH.
export function AddNodeDialog({
  onClose,
  onCreated,
  onDone,
}: {
  onClose: () => void;
  onCreated: (command: string) => void;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<"command" | "ssh">("command");
  const [name, setName] = useState("");
  const [host, setHost] = useState("");
  const [busy, setBusy] = useState(false);

  // SSH (auto) fields.
  const [sshHost, setSshHost] = useState("");
  const [sshPort, setSshPort] = useState("22");
  const [sshUser, setSshUser] = useState("root");
  const [sshAuth, setSshAuth] = useState<"password" | "key">("password");
  const [sshPassword, setSshPassword] = useState("");
  const [sshKey, setSshKey] = useState("");
  const [log, setLog] = useState<string[]>([]);
  const [installing, setInstalling] = useState(false);
  // The node is created once; a retry after a failed SSH install reuses this id
  // instead of creating a second orphan node.
  const [createdId, setCreatedId] = useState<number | null>(null);

  const submitCommand = async () => {
    if (!name.trim() || !host.trim()) return;
    setBusy(true);
    try {
      const res = await createNode(name.trim(), host.trim());
      onCreated(res.install_command);
    } catch (e) {
      notifyError(errMessage(e));
      setBusy(false);
    }
  };

  const submitSSH = async () => {
    if (!name.trim() || !host.trim() || !sshHost.trim()) return;
    if (sshAuth === "password" && !sshPassword) return;
    if (sshAuth === "key" && !sshKey.trim()) return;
    setInstalling(true);
    try {
      // Create the node once; on a retry reuse the existing id so a failed install
      // doesn't leave a trail of orphan not-joined nodes.
      let nodeId = createdId;
      if (nodeId == null) {
        setLog([t("nodes.creating")]);
        const res = await createNode(name.trim(), host.trim());
        nodeId = res.id;
        setCreatedId(res.id);
      } else {
        setLog([t("nodes.reinstalling")]);
      }
      const outcome = await provisionNode(
        nodeId,
        {
          ssh_host: sshHost.trim(),
          ssh_port: Number(sshPort) || 22,
          ssh_user: sshUser.trim(),
          ssh_password: sshAuth === "password" ? sshPassword : undefined,
          ssh_key: sshAuth === "key" ? sshKey : undefined,
        },
        (line) => setLog((l) => [...l, line]),
      );
      if (outcome === "done") {
        notifySuccess(t("nodes.installedOverSsh"));
        onDone();
      } else {
        notifyError(t("nodes.installFailed"));
        setInstalling(false);
      }
    } catch (e) {
      setLog((l) => [...l, `${ERR_PREFIX}: ${errMessage(e)}`]);
      notifyError(errMessage(e));
      setInstalling(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t("nodes.addNode")}
      size="lg"
      dismissible={!installing}
    >
      <div className="mb-4 inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
        {(["command", "ssh"] as const).map((m) => (
          <button
            type="button"
            key={m}
            onClick={() => setMode(m)}
            disabled={installing}
            className={cn(
              "rounded-md px-3 py-1 transition",
              mode === m ? "bg-brand-600 text-onbrand" : "text-ink-muted",
            )}
          >
            {t(m === "command" ? "nodes.tabCommand" : "nodes.tabSsh")}
          </button>
        ))}
      </div>

      <div className="space-y-3">
        <TextInput label={t("groups.name")} value={name} onChange={setName} placeholder={t("nodes.namePlaceholder")} />
        <TextInput
          label={t("nodes.hostLabel")}
          value={host}
          onChange={setHost}
          placeholder="nl1.example.com"
        />

        {mode === "ssh" && (
          <div className="space-y-3 border-t border-gray-100 pt-3">
            <p className="text-xs text-ink-muted">
              {t("nodes.sshHint")}
            </p>
            <div className="grid grid-cols-3 gap-2">
              <div className="col-span-2">
                <TextInput label={t("nodes.sshHost")} value={sshHost} onChange={setSshHost} placeholder="203.0.113.10" />
              </div>
              <TextInput label={t("conn.port")} value={sshPort} onChange={setSshPort} placeholder="22" />
            </div>
            <TextInput label={t("nodes.sshUser")} value={sshUser} onChange={setSshUser} placeholder="root" />
            <div className="inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
              {(["password", "key"] as const).map((a) => (
                <button
                  type="button"
                  key={a}
                  onClick={() => setSshAuth(a)}
                  className={cn(
                    "rounded-md px-3 py-1 transition",
                    sshAuth === a ? "bg-brand-600 text-onbrand" : "text-ink-muted",
                  )}
                >
                  {t(a === "password" ? "login.password" : "nodes.key")}
                </button>
              ))}
            </div>
            {sshAuth === "password" ? (
              <PasswordInput label={t("nodes.sshPassword")} value={sshPassword} onChange={setSshPassword} />
            ) : (
              <Textarea
                label={t("nodes.privateKey")}
                value={sshKey}
                onChange={setSshKey}
                rows={4}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              />
            )}
          </div>
        )}

        {log.length > 0 && (
          <div className="max-h-56 overflow-auto rounded-md bg-gray-50 p-3 font-mono text-xs">
            {log.map((l, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: an install transcript is positional — lines repeat verbatim and only ever append
              <div key={i} className={l.startsWith(ERR_PREFIX) ? "text-danger" : ""}>
                {l}
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose} disabled={installing}>
          {t("common.cancel")}
        </Button>
        {mode === "command" ? (
          <Button onClick={submitCommand} loading={busy} disabled={!name.trim() || !host.trim()}>
            {t("common.create")}
          </Button>
        ) : (
          <Button
            onClick={submitSSH}
            loading={installing}
            disabled={!name.trim() || !host.trim() || !sshHost.trim()}
          >
            {t("nodes.install")}
          </Button>
        )}
      </div>
    </Modal>
  );
}

// ReconnectDialog re-installs a node that isn't connected — it SSHes back into the
// server and re-runs the install with a fresh token, streaming the log (which also
// surfaces why the previous attempt didn't connect). SSH creds aren't stored.
export function ReconnectDialog({
  node,
  onClose,
  onDone,
  onRegen,
}: {
  node: NodeView;
  onClose: () => void;
  onDone: () => void;
  onRegen: (command: string) => void;
}) {
  // Both tabs reinstall the node; they differ only in who runs the installer. The
  // command tab revokes the node's current token (the old install stops connecting
  // until the command is run), SSH keeps it until the new install succeeds.
  const { t } = useTranslation();
  const [mode, setMode] = useState<"command" | "ssh">("command");
  const [busy, setBusy] = useState(false);
  const [sshHost, setSshHost] = useState(node.host);
  const [sshPort, setSshPort] = useState("22");
  const [sshUser, setSshUser] = useState("root");
  const [sshAuth, setSshAuth] = useState<"password" | "key">("password");
  const [sshPassword, setSshPassword] = useState("");
  const [sshKey, setSshKey] = useState("");
  const [log, setLog] = useState<string[]>([]);
  const [running, setRunning] = useState(false);

  // Command tab: mint a fresh install token and hand the one-liner to the parent,
  // which shows it once (it is a credential — never rendered twice).
  const issueCommand = async () => {
    setBusy(true);
    try {
      const res = await regenNodeJoin(node.id);
      onRegen(res.install_command);
      onClose();
    } catch (e) {
      notifyError(errMessage(e));
      setBusy(false);
    }
  };

  const run = async () => {
    if (!sshHost.trim()) return;
    if (sshAuth === "password" && !sshPassword) return;
    if (sshAuth === "key" && !sshKey.trim()) return;
    setRunning(true);
    setLog([t("nodes.reinstallingNode")]);
    try {
      const outcome = await provisionNode(
        node.id,
        {
          ssh_host: sshHost.trim(),
          ssh_port: Number(sshPort) || 22,
          ssh_user: sshUser.trim(),
          ssh_password: sshAuth === "password" ? sshPassword : undefined,
          ssh_key: sshAuth === "key" ? sshKey : undefined,
        },
        (line) => setLog((l) => [...l, line]),
      );
      if (outcome === "done") {
        notifySuccess(t("nodes.reinstalled"));
        onDone();
      } else {
        notifyError(t("nodes.failedSeeLog"));
        setRunning(false);
      }
    } catch (e) {
      setLog((l) => [...l, `${ERR_PREFIX}: ${errMessage(e)}`]);
      notifyError(errMessage(e));
      setRunning(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t("nodes.reinstallOf", { name: node.name })}
      size="lg"
      dismissible={!running}
    >
      <div className="mb-4 inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
        {(["command", "ssh"] as const).map((m) => (
          <button
            type="button"
            key={m}
            onClick={() => setMode(m)}
            disabled={running}
            className={cn(
              "rounded-md px-3 py-1 transition",
              mode === m ? "bg-brand-600 text-onbrand" : "text-ink-muted",
            )}
          >
            {t(m === "command" ? "nodes.tabCommand" : "nodes.tabReinstallSsh")}
          </button>
        ))}
      </div>

      {mode === "command" ? (
        <p className="text-sm text-ink-muted">
          {t("nodes.reinstallCommandHint")}
        </p>
      ) : (
        <div className="space-y-3">
          <p className="text-xs text-ink-muted">
            {t("nodes.reinstallSshHint")}
          </p>
          <div className="grid grid-cols-3 gap-2">
            <div className="col-span-2">
              <TextInput label={t("nodes.sshHost")} value={sshHost} onChange={setSshHost} placeholder="203.0.113.10" />
            </div>
            <TextInput label={t("conn.port")} value={sshPort} onChange={setSshPort} placeholder="22" />
          </div>
          <TextInput label={t("nodes.sshUser")} value={sshUser} onChange={setSshUser} placeholder="root" />
          <div className="inline-flex rounded-lg border border-gray-200 p-0.5 text-sm">
            {(["password", "key"] as const).map((a) => (
              <button
                type="button"
                key={a}
                onClick={() => setSshAuth(a)}
                className={cn(
                  "rounded-md px-3 py-1 transition",
                  sshAuth === a ? "bg-brand-600 text-onbrand" : "text-ink-muted",
                )}
              >
                {t(a === "password" ? "login.password" : "nodes.key")}
              </button>
            ))}
          </div>
          {sshAuth === "password" ? (
            <PasswordInput label={t("nodes.sshPassword")} value={sshPassword} onChange={setSshPassword} />
          ) : (
            <Textarea
              label={t("nodes.privateKey")}
              value={sshKey}
              onChange={setSshKey}
              rows={4}
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
            />
          )}
          {log.length > 0 && (
            <div className="max-h-56 overflow-auto rounded-md bg-gray-50 p-3 font-mono text-xs">
              {log.map((l, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: an install transcript is positional — lines repeat verbatim and only ever append
                <div key={i} className={l.startsWith(ERR_PREFIX) ? "text-danger" : ""}>
                  {l}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="light" color="gray" onClick={onClose} disabled={running}>
          {t("common.cancel")}
        </Button>
        {mode === "command" ? (
          <Button onClick={issueCommand} loading={busy}>
            {t("nodes.getCommand")}
          </Button>
        ) : (
          <Button onClick={run} loading={running} disabled={!sshHost.trim()}>
            {t("nodes.reinstall")}
          </Button>
        )}
      </div>
    </Modal>
  );
}
