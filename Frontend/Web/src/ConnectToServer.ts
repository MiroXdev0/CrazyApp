export async function connectToServer(url: string) {
  const response = await fetch(url);
  return response.json();
}
