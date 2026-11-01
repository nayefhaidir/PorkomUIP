import { mkdir, readFile, unlink, writeFile } from "node:fs/promises";
import path from "node:path";

export interface FileStorage {
  put(key: string, data: Uint8Array): Promise<void>;
  get(key: string): Promise<Buffer>;
  remove(key: string): Promise<void>;
}

class LocalFileStorage implements FileStorage {
  private readonly root = path.resolve(process.env.UPLOAD_DIR ?? "./uploads");
  private resolveKey(key: string) {
    if (!/^[a-f0-9-]{36}\.(pdf|png|jpg|docx|xlsx)$/.test(key)) throw new Error("Invalid storage key");
    return path.join(this.root, key);
  }
  async put(key: string, data: Uint8Array) {
    await mkdir(this.root, { recursive: true, mode: 0o700 });
    await writeFile(this.resolveKey(key), data, { mode: 0o600, flag: "wx" });
  }
  async get(key: string) { return readFile(this.resolveKey(key)); }
  async remove(key: string) { await unlink(this.resolveKey(key)); }
}

export const fileStorage: FileStorage = new LocalFileStorage();
