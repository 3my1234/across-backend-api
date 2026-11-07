
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			offer.currency_code,
			CASE WHEN p.is_flash_sale AND offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price AND p.flash_sale_price > 0 AND p.flash_sale_price < p.local_selling_price THEN p.flash_sale_price+offer.delivery_fee ELSE offer.delivered_price END,
			CASE WHEN offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price THEN CASE WHEN p.is_flash_sale AND p.flash_sale_price>0 AND p.flash_sale_price<p.local_selling_price THEN GREATEST(p.local_selling_price,COALESCE(p.compare_at_price,0))+offer.delivery_fee WHEN p.compare_at_price>p.local_selling_price THEN p.compare_at_price+offer.delivery_fee ELSE 0 END ELSE 0 END,
			p.inventory_count,
			p.factory_details || jsonb_build_object('payment_mode',p.payment_mode,'catalog_updated_at',p.updated_at,'catalog_version',p.catalog_version,'provider_name',COALESCE((SELECT seller.business_name FROM provider_organizations seller WHERE seller.id=p.provider_id),''),'inventory_country_code',p.inventory_country_code,'inventory_city',p.inventory_city,'inventory_location',p.inventory_location,'stock_state',p.stock_state,'handling_time_hours',p.handling_time_hours,'delivery_min_days',p.delivery_min_days,'delivery_max_days',p.delivery_max_days,'delivery_methods',p.delivery_methods,'delivery_areas',COALESCE((SELECT jsonb_agg(jsonb_build_object('country_code',a.country_code,'state',a.state_key,'city',a.city_key,'delivered_price',a.delivered_price,'delivery_fee',a.delivery_fee,'currency_code',a.currency_code)) FROM product_delivery_areas a WHERE a.product_id=p.id),'[]'::jsonb)),
			COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, ''),
			(p.is_flash_sale AND offer.currency_code=p.local_currency_code AND offer.delivered_price-offer.delivery_fee=p.local_selling_price), COALESCE(p.flash_sale_price+offer.delivery_fee, 0), p.review_count, p.sold_count,
			CASE WHEN p.review_count > 0 THEN p.review_rating_sum::float8 / p.review_count ELSE 0 END,
			p.provider_id::text,p.fulfillment_mode,
			CASE WHEN $1::boolean AND p.inventory_country_code=$5 AND p.stock_state<>'import_on_demand' AND p.inventory_latitude IS NOT NULL AND p.inventory_longitude IS NOT NULL THEN 6371 * 2 * asin(sqrt(power(sin(radians((p.inventory_latitude::float8-$2::float8)/2)),2)+cos(radians($2::float8))*cos(radians(p.inventory_latitude::float8))*power(sin(radians((p.inventory_longitude::float8-$3::float8)/2)),2))) END AS distance_km
		FROM products p
        LEFT JOIN provider_public_identity visibility ON visibility.provider_id=p.provider_id
		JOIN LATERAL (SELECT a.delivered_price,a.delivery_fee,a.currency_code FROM product_delivery_areas a WHERE a.product_id=p.id AND a.country_code=$5 AND a.currency_code=(SELECT market.currency_code FROM countries_config market WHERE market.country_code=$5 AND market.is_active=true AND 'flutterwave'=ANY(market.active_payment_gateways)) AND (a.state_key='' OR a.state_key=$6) AND (a.city_key='' OR a.city_key=$7) ORDER BY (a.city_key<>'') DESC,(a.state_key<>'') DESC LIMIT 1) offer ON TRUE
		LEFT JOIN logistics_hubs lh ON lh.id = p.origin_hub_id
		WHERE p.is_active = true AND p.moderation_status='approved' AND (p.provider_id IS NULL OR p.plan_slot<=COALESCE(visibility.listing_limit,1))
		  AND p.provider_id IS NOT NULL AND p.fulfillment_mode IN ('merchant_local','merchant_cross_border')
		  AND ($8::text NOT IN ('local','international') OR ($8='local' AND p.inventory_country_code=$5) OR ($8='international' AND p.inventory_country_code<>$5))
		  AND EXISTS(SELECT 1 FROM provider_organizations po WHERE po.id=p.provider_id AND po.verification_status='approved' AND po.is_active=true AND ($4::boolean OR EXISTS(SELECT 1 FROM provider_subscriptions ps WHERE ps.provider_id=po.id AND ps.status='active' AND ps.current_period_end>now())))
		  AND (p.payment_mode='contact' OR EXISTS(SELECT 1 FROM provider_payout_accounts pa WHERE pa.provider_id=p.provider_id AND pa.payment_provider='flutterwave' AND pa.status='active'))
		ORDER BY CASE WHEN COALESCE(visibility.verified AND visibility.paid_active,false) THEN CASE visibility.badge_tier WHEN 'gold' THEN 3 WHEN 'diamond' THEN 3 WHEN 'silver' THEN 2 ELSE 1 END ELSE 0 END DESC, CASE WHEN p.inventory_country_code=$5 THEN 0 ELSE 1 END, distance_km ASC NULLS LAST, p.created_at DESC
		LIMIT 80
	