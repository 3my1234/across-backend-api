"""Offline launch budgets. Reads explicit non-secret JSON; never contacts a service."""
import argparse
import json
import math
from pathlib import Path


def number(value, name, minimum=0):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < minimum:
        raise ValueError(f"{name} must be a finite number >= {minimum}")
    return value


def integer(value, name, minimum=0):
    number(value, name, minimum)
    if not isinstance(value, int):
        raise ValueError(f"{name} must be an integer")
    return value


def connection_budget(config):
    total = 0
    rows = []
    for service in config["services"]:
        instances = integer(service["instances"], "instances")
        primary = integer(service["primary_max"], "primary_max")
        # A read URL pointing at the SAME pgx pool must not be counted twice.
        separate = service.get("separate_read_pool", False)
        if not isinstance(separate, bool):
            raise ValueError("separate_read_pool must be boolean")
        read = integer(service.get("read_max", 0), "read_max") if separate else 0
        maximum = instances * (primary + read)
        total += maximum
        rows.append(dict(service=service["name"], maximum_connections=maximum))
    server = integer(config["postgres_max_connections"], "postgres_max_connections", 1)
    reserved = integer(config["admin_and_emergency_reserve"], "admin_and_emergency_reserve", 1)
    other = integer(config["other_clients_max"], "other_clients_max")
    demand = total + other + reserved
    verified = config.get("inventory_verified") is True
    return dict(services=rows, application_connections=total, other_clients=other,
                administration_reserve=reserved, required_connections=demand,
                postgres_max_connections=server, spare_connections=server-demand,
                fits=demand <= server, inventory_verified=verified,
                production_budget_verified=verified and demand <= server)


def media_budget(config):
    # Prices, exchange rate and usage are caller-supplied assumptions, NOT a live quote.
    rates = config["rates_usd"]
    for key in ("storage_gb_month", "put_1000", "get_1000", "origin_egress_gb", "cdn_egress_gb", "encoding_hour"):
        number(rates[key], key)
    fx = number(config["ngn_per_usd"], "ngn_per_usd", 0.000001)
    shares = config["tier_shares"]
    if set(shares) != {"free", "bronze", "silver", "diamond", "gold"}:
        raise ValueError("all five tier shares are required")
    for value in shares.values():
        number(value, "tier share")
    if not math.isclose(sum(shares.values()), 1.0, abs_tol=1e-9):
        raise ValueError("tier shares must total 1")
    use = config["usage"]
    for key in ("images_per_provider", "image_bytes", "videos_per_gold_provider", "video_bytes", "temporary_storage_fraction", "monthly_replacements_per_asset", "encoding_seconds_per_video", "video_view_fraction"):
        number(use[key], key)
    if use["videos_per_gold_provider"] > 5 or use["video_view_fraction"] > 1:
        raise ValueError("video usage exceeds the proposed launch policy")
    image_variants = integer(use.get("image_variants",1), "image variants", 1)
    image_stored_bytes = number(use.get("image_stored_bytes",use["image_bytes"]), "stored image variant bytes")
    prices = {"free": 0, "bronze": 500, "silver": 1000, "diamond": 2000, "gold": 5000}
    rows = []
    for count in config["provider_counts"]:
        integer(count, "providers", 1)
        images = count * use["images_per_provider"]
        videos = count * shares["gold"] * use["videos_per_gold_provider"]
        stored_gb = (images*image_stored_bytes + videos*use["video_bytes"]) / 1e9
        stored_gb *= 1 + use["temporary_storage_fraction"]
        replacements = use["monthly_replacements_per_asset"]
        puts = (images*image_variants + videos) * replacements
        encode_hours = videos * replacements * use["encoding_seconds_per_video"] / 3600
        for workload in config["viewing_scenarios"]:
            views = count * number(workload["asset_views_per_provider_month"], "views")
            video_views = views * use["video_view_fraction"]
            bytes_delivered = (views-video_views)*use["image_bytes"] + video_views*use["video_bytes"]
            for hit in config["cache_hit_rates"]:
                number(hit, "cache hit rate")
                if hit > 1:
                    raise ValueError("cache hit rate must not exceed 1")
                viewer_gb = bytes_delivered/1e9
                video_hit = number(config.get("video_cache_hit_rate",0),"video cache hit rate")
                if video_hit>1:raise ValueError("video cache hit rate must not exceed1")
                video_gb=video_views*use["video_bytes"]/1e9
                image_gb=viewer_gb-video_gb
                origin_gb=image_gb*(1-hit)+video_gb*(1-video_hit)
                gets=(views-video_views)*(1-hit)+video_views*(1-video_hit)
                video_cdn_price=number(rates.get("video_cdn_egress_gb",0),"video CDN price")
                parts = dict(storage=stored_gb*rates["storage_gb_month"],
                             puts=puts/1000*rates["put_1000"],
                             gets=gets/1000*rates["get_1000"],
                             origin_egress=origin_gb*rates["origin_egress_gb"],
                             cdn_egress=image_gb*rates["cdn_egress_gb"]+video_gb*video_cdn_price,
                             encoding=encode_hours*rates["encoding_hour"])
                usd=sum(parts.values())
                rows.append(dict(providers=count, viewing=workload["name"], cache_hit_rate=hit,
                                 stored_gb=stored_gb, delivered_gb=viewer_gb, video_delivered_gb=video_gb, video_cache_hit_rate=video_hit, origin_gb=origin_gb,
                                 monthly_media_usd=usd, monthly_media_ngn=usd*fx,
                                 gross_subscription_ngn=count*sum(shares[t]*prices[t] for t in shares),
                                 components_usd=parts))
    return dict(scope="planning assumptions; not verified billing, capacity, profit or a CDN-contract approval",
                region=config["region"], prices_verified=config.get("prices_verified") is True,
                excludes=["payment fees", "taxes", "VPS baseline", "support", "monitoring", "additional retained versions/backups", "abusive uploads beyond modeled replacement rate"],
                scenarios=rows)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind",choices=["connections", "media"])
    parser.add_argument("config",type=Path)
    args=parser.parse_args()
    config=json.loads(args.config.read_text(encoding="utf-8-sig"))
    result=connection_budget(config) if args.kind=="connections" else media_budget(config)
    print(json.dumps(result,indent=2))
    if args.kind=="connections" and not result["fits"]:
        raise SystemExit(1)

if __name__=="__main__":
    main()
