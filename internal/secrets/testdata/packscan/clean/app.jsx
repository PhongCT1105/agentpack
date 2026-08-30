function ItemList({ items }) {
  return (
    <ul>
      {items.map((item) => (
        <li key={item.userId}>{item.name}</li>
      ))}
    </ul>
  );
}
