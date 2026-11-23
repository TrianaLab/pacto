/** saveFile hands text to the browser as a download named name, through a blob URL
 *  revoked once the save has had time to start. */
export function saveFile(name: string, text: string, type = 'application/yaml'): void {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  // ponytail: a fixed second; some browsers drop a download whose URL is revoked in the same task.
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
