const fullDockerIdPattern = /^[a-f0-9]{64}$/i

export function displayDockerId(value: string) {
  return fullDockerIdPattern.test(value) ? value.slice(0, 12) : value
}
