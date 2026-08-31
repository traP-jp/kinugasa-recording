interface MoblinSettings {
  streams: Array<{
    name: string;
    url: string;
    selected: true;
    video: {
      codec: "H.264/AVC";
      fps: 30;
    };
  }>;
}

export function buildMoblinUrl(name: string, url: string): string {
  const settings: MoblinSettings = {
    streams: [{
      name,
      url,
      selected: true,
      video: {
        codec: "H.264/AVC",
        fps: 30,
      },
    }],
  };

  return `moblin://?${encodeURIComponent(JSON.stringify(settings))}`;
}
