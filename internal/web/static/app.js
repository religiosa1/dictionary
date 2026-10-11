// Clicking a suggested tag on the phrase form appends it to the tags input,
// unless it's already there. Delegated on document, so it keeps working after
// htmx swaps the page.
document.addEventListener("click", (event) => {
  const button = event.target.closest(".tag-suggestion");
  const input = document.getElementById("tags");
  if (!button || !input) {
    return;
  }
  // Same folding as the server: lowercase, collapsed whitespace.
  const normalize = (tag) => tag.trim().toLowerCase().split(/\s+/).join(" ");
  const tags = input.value.split(",").map(normalize).filter(Boolean);
  const tag = normalize(button.dataset.tag);
  if (!tags.includes(tag)) {
    tags.push(tag);
  }
  input.value = tags.join(", ");
  input.focus();
});
