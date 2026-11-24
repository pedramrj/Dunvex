// Passkeys in the browser: WebAuthn's binary fields travel as base64url JSON
// between the panel and here (the panel's go-webauthn speaks that form).
import { api } from "./api";

function enc(buf: ArrayBuffer | null | undefined): string {
  if (!buf) return "";
  let s = "";
  for (const b of new Uint8Array(buf)) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function dec(s: string): ArrayBuffer {
  const b = atob(s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4));
  const out = new Uint8Array(b.length);
  for (let i = 0; i < b.length; i++) out[i] = b.charCodeAt(i);
  return out.buffer;
}

const isIP = (h: string) => /^\d{1,3}(\.\d{1,3}){3}$/.test(h) || h.includes(":");

/** Whether this page can use passkeys: a browser with WebAuthn, on a name over HTTPS (or localhost). */
export function passkeysPossible(): boolean {
  if (typeof window === "undefined" || !window.PublicKeyCredential || !navigator.credentials) return false;
  const h = location.hostname;
  if (!h || isIP(h)) return false;
  return location.protocol === "https:" || h === "localhost";
}

type Descriptor = { id: string; type: "public-key"; transports?: AuthenticatorTransport[] };

function createOptions(o: any): CredentialCreationOptions {
  const p = o.publicKey;
  return {
    publicKey: {
      ...p,
      challenge: dec(p.challenge),
      user: { ...p.user, id: dec(p.user.id) },
      excludeCredentials: (p.excludeCredentials ?? []).map((c: Descriptor) => ({ ...c, id: dec(c.id) })),
    },
  };
}

function getOptions(o: any): CredentialRequestOptions {
  const p = o.publicKey;
  return {
    publicKey: {
      ...p,
      challenge: dec(p.challenge),
      allowCredentials: (p.allowCredentials ?? []).map((c: Descriptor) => ({ ...c, id: dec(c.id) })),
    },
  };
}

function credentialJSON(c: PublicKeyCredential): unknown {
  const r = c.response as AuthenticatorAttestationResponse & AuthenticatorAssertionResponse;
  const response: Record<string, unknown> = { clientDataJSON: enc(r.clientDataJSON) };
  if ("attestationObject" in r && r.attestationObject) {
    response.attestationObject = enc(r.attestationObject);
    response.transports = typeof r.getTransports === "function" ? r.getTransports() : [];
  } else {
    response.authenticatorData = enc(r.authenticatorData);
    response.signature = enc(r.signature);
    response.userHandle = enc(r.userHandle);
  }
  return {
    id: c.id, rawId: enc(c.rawId), type: c.type, response,
    clientExtensionResults: c.getClientExtensionResults?.() ?? {},
    authenticatorAttachment: c.authenticatorAttachment ?? undefined,
  };
}

/** What went wrong in the browser's own step, as a key of the dictionaries. */
export function passkeyErrorKey(e: unknown): "pkCancelled" | "pkAlready" | "pkWrongSite" | null {
  const name = (e as { name?: string })?.name;
  if (name === "NotAllowedError" || name === "AbortError") return "pkCancelled";
  if (name === "InvalidStateError") return "pkAlready";
  if (name === "SecurityError") return "pkWrongSite";
  return null;
}

/** Signs in with any passkey this browser has for the panel's domain. */
export async function signInWithPasskey(): Promise<void> {
  const b = await api<{ token: string; options: unknown }>("login/passkey/begin", { body: {} });
  const cred = (await navigator.credentials.get(getOptions(b.options))) as PublicKeyCredential | null;
  if (!cred) throw Object.assign(new Error("cancelled"), { name: "NotAllowedError" });
  await api("login/passkey/finish", { body: { token: b.token, credential: credentialJSON(cred) } });
}

/** Makes a new passkey for the signed-in admin; the current password proves it is them. */
export async function addPasskey(password: string, name: string): Promise<void> {
  const b = await api<{ token: string; options: unknown }>("account/passkeys/begin", { body: { password } });
  const cred = (await navigator.credentials.create(createOptions(b.options))) as PublicKeyCredential | null;
  if (!cred) throw Object.assign(new Error("cancelled"), { name: "NotAllowedError" });
  await api("account/passkeys/finish", { body: { token: b.token, name, credential: credentialJSON(cred) } });
}
